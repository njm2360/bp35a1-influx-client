package bp35a1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.bug.st/serial"

	"main/internal/transport"
)

const echonetPort = 3610

const defaultBaud = 115200

var candidateBauds = []int{115200, 2400, 4800, 9600, 19200, 38400, 57600}

const (
	cmdSKSREG    = "SKSREG"
	cmdSKSCAN    = "SKSCAN"
	cmdSKLL64    = "SKLL64"
	cmdSKJOIN    = "SKJOIN"
	cmdSKSENDTO  = "SKSENDTO"
	cmdSKSETRBID = "SKSETRBID"
	cmdSKSETPWD  = "SKSETPWD"
	cmdSKVER     = "SKVER"
	cmdSKRESET   = "SKRESET"
	cmdROPT      = "ROPT"
	cmdWOPT      = "WOPT"
)

const (
	evActiveScanOK   = 0x22
	evPANAConnectErr = 0x24
	evPANAConnectOK  = 0x25
	evLifetimeExpire = 0x29
)

var (
	ErrTxProhibited = errors.New("bp35a1: UDP transmit prohibited (no PANA session)")
	ErrPANAConnect  = errors.New("bp35a1: PANA connection failed")
	ErrClosed       = errors.New("bp35a1: device closed")
)

type rxState int

const (
	stateNormal rxState = iota
	stateEpandesc
	stateSKLL64
	stateProductRead
)

type Options struct {
	Port      string
	Baud      int
	RouteBID  string
	Password  string
	EpanCache string
	Logger    *slog.Logger
}

type skEvent struct {
	code   int
	sender string
}

type Device struct {
	port serial.Port
	log  *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	cmdMu sync.Mutex

	rxMu    sync.Mutex
	newline string
	state   rxState

	bufMu sync.Mutex
	buf   []byte

	results   chan string
	responses chan string
	events    chan skEvent
	epans     chan Epan
	rxudp     chan []byte

	epan     Epan
	epanSeen map[string]bool

	epanCache string

	txAllowed atomic.Bool
	ip        atomic.Value // string

	// reconnect は manage が PANA セッション期限切れを検知した際に呼ぶ。
	// 既定では reestablish。テストで差し替え可能。
	// 戻り値: 次回以降に使う EPAN と、監視を継続するかどうか。
	reconnect func(Epan) (Epan, bool)

	closeOnce sync.Once
	closed    chan struct{}
}

func (d *Device) setIP(ip string) { d.ip.Store(ip) }

func (d *Device) getIP() string {
	if v, ok := d.ip.Load().(string); ok {
		return v
	}
	return ""
}

var _ transport.Transport = (*Device)(nil)

func Open(ctx context.Context, opts Options) (*Device, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	baud := opts.Baud
	if baud == 0 {
		baud = defaultBaud
	}

	port, err := serial.Open(opts.Port, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, fmt.Errorf("bp35a1: open serial: %w", err)
	}
	if err := port.SetReadTimeout(200 * time.Millisecond); err != nil {
		port.Close()
		return nil, fmt.Errorf("bp35a1: set read timeout: %w", err)
	}

	dctx, cancel := context.WithCancel(context.Background())
	d := &Device{
		port:      port,
		log:       opts.Logger,
		ctx:       dctx,
		cancel:    cancel,
		newline:   "\r\n",
		state:     stateNormal,
		epanCache: opts.EpanCache,
		results:   make(chan string, 8),
		responses: make(chan string, 32),
		events:    make(chan skEvent, 16),
		epans:     make(chan Epan, 4),
		rxudp:     make(chan []byte, 8),
		closed:    make(chan struct{}),
	}
	d.reconnect = d.reestablish

	go d.readLoop()

	if err := d.setup(ctx, opts, baud); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *Device) setup(ctx context.Context, opts Options, baud int) error {
	if err := d.initModule(ctx, opts, baud); err != nil {
		return err
	}

	epan, ok := loadEpan(opts.EpanCache)
	if ok {
		d.log.Info("using cached EPAN", "channel", epan.Channel, "pan_id", epan.PanID)
	} else {
		scanned, err := d.scan(ctx, 6)
		if err != nil {
			return err
		}
		epan = *scanned
		if err := saveEpan(opts.EpanCache, epan); err != nil {
			d.log.Warn("failed to cache EPAN", "err", err)
		}
	}

	ip, err := d.connect(ctx, epan)
	if err != nil {
		return err
	}
	d.setIP(ip)
	d.log.Info("PANA connected", "ip", ip)

	// 接続確立後に常駐監視を開始する。これ以降 d.events の唯一の消費者となり、
	// PANA セッション期限切れ(EVENT 0x29)を検知して再接続する。
	go d.manage(epan)
	return nil
}

// manage は接続確立後に常駐し、d.events を消費する。
// PANA セッション期限切れを検知したら txAllowed を落として再接続する。
// connect/scan の events 読み取りはこのゴルーチンが直列に実行するため競合しない。
func (d *Device) manage(epan Epan) {
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-d.closed:
			return
		case ev := <-d.events:
			if ev.code == evLifetimeExpire {
				d.log.Warn("PANA session expired; reconnecting")
				d.txAllowed.Store(false)
				next, ok := d.reconnect(epan)
				if !ok {
					return
				}
				epan = next
			}
		}
	}
}

func (d *Device) reestablish(epan Epan) (Epan, bool) {
	const maxBackoff = 5 * time.Minute
	const rescanAfterFailures = 5

	backoff := time.Second
	for attempt := 1; ; attempt++ {
		if d.ctx.Err() != nil {
			return epan, false
		}
		if attempt > 1 && (attempt-1)%rescanAfterFailures == 0 {
			if scanned, err := d.scan(d.ctx, 6); err != nil {
				d.log.Warn("rescan during reconnect failed", "err", err)
			} else {
				epan = *scanned
				if err := saveEpan(d.epanCache, epan); err != nil {
					d.log.Warn("failed to cache EPAN", "err", err)
				}
			}
		}

		ip, err := d.connect(d.ctx, epan)
		if err == nil {
			d.setIP(ip)
			d.log.Info("PANA reconnected", "ip", ip, "attempt", attempt)
			return epan, true
		}
		if d.ctx.Err() != nil {
			return epan, false
		}
		d.log.Warn("reconnect attempt failed", "attempt", attempt, "err", err)

		select {
		case <-d.ctx.Done():
			return epan, false
		case <-d.closed:
			return epan, false
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (d *Device) initModule(ctx context.Context, opts Options, baud int) error {
	if err := d.correctBaudrate(ctx, baud); err != nil {
		return err
	}
	if _, err := d.command(ctx, cmdSKRESET, nil, 3*time.Second, true); err != nil {
		return err
	}
	if _, err := d.command(ctx, cmdSKSREG, []string{"SFE", "0"}, time.Second, true); err != nil {
		return err
	}

	opt, err := d.command(ctx, cmdROPT, nil, time.Second, false)
	if err != nil {
		return err
	}
	if opt != "01" {
		if _, err := d.command(ctx, cmdWOPT, []string{"01"}, time.Second, false); err != nil {
			return err
		}
	}

	rbid := strings.ReplaceAll(opts.RouteBID, "-", "")
	if _, err := d.command(ctx, cmdSKSETRBID, []string{rbid}, time.Second, false); err != nil {
		return err
	}
	pwd := opts.Password
	if _, err := d.command(ctx, cmdSKSETPWD, []string{fmt.Sprintf("%X", len(pwd)), pwd}, time.Second, false); err != nil {
		return err
	}
	return nil
}

func (d *Device) correctBaudrate(ctx context.Context, preferred int) error {
	bauds := append([]int{preferred}, candidateBauds...)

	// タイミングによりSKVERが稀にFAILするため2ループ試す
	for range 2 {
		for _, b := range bauds {
			if err := d.port.SetMode(&serial.Mode{BaudRate: b}); err != nil {
				continue
			}
			d.log.Debug("testing baudrate", "baud", b)

			d.clearBuffer()
			d.traceSerial("tx", []byte("\r\n"))
			_, _ = d.port.Write([]byte("\r\n"))
			_ = d.port.ResetInputBuffer()
			_ = d.port.ResetOutputBuffer()

			resp, err := d.command(ctx, cmdSKVER, nil, time.Second, true)
			if err == nil && strings.HasPrefix(resp, "EVER") {
				d.log.Info("baudrate detected", "baud", b)
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	return errors.New("bp35a1: no valid baudrate found")
}

func (d *Device) scan(ctx context.Context, initDuration int) (*Epan, error) {
	d.log.Info("scanning for smart meter")
	for duration := initDuration; duration <= 7; duration++ {
		if _, err := d.command(ctx, cmdSKSCAN, []string{"2", "FFFFFFFF", strconv.Itoa(duration)}, time.Second, false); err != nil {
			return nil, err
		}
		var found *Epan
	wait:
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-d.closed:
				return nil, ErrClosed
			case e := <-d.epans:
				found = &e
			case ev := <-d.events:
				if ev.code == evActiveScanOK {
					found = drainEpan(d.epans, found)
					break wait
				}
			case <-time.After(30 * time.Second):
				break wait
			}
		}
		if found != nil {
			return found, nil
		}
	}
	return nil, errors.New("bp35a1: EPAN not found")
}

func drainEpan(ch <-chan Epan, cur *Epan) *Epan {
	for {
		select {
		case e := <-ch:
			cur = &e
		default:
			return cur
		}
	}
}

func (d *Device) connect(ctx context.Context, epan Epan) (string, error) {
	if _, err := d.command(ctx, cmdSKSREG, []string{"S2", fmt.Sprintf("%X", epan.Channel)}, time.Second, false); err != nil {
		return "", err
	}
	if _, err := d.command(ctx, cmdSKSREG, []string{"S3", fmt.Sprintf("%X", epan.PanID)}, time.Second, false); err != nil {
		return "", err
	}
	ip, err := d.command(ctx, cmdSKLL64, []string{epan.MACAddress}, time.Second, false)
	if err != nil {
		return "", err
	}
	if _, err := d.command(ctx, cmdSKJOIN, []string{ip}, time.Second, false); err != nil {
		return "", err
	}

	d.log.Info("waiting for PANA connection", "ip", ip)
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-d.closed:
			return "", ErrClosed
		case ev := <-d.events:
			switch ev.code {
			case evPANAConnectOK:
				return ip, nil
			case evPANAConnectErr:
				return "", ErrPANAConnect
			}
		case <-time.After(30 * time.Second):
			return "", errors.New("bp35a1: PANA connect timeout")
		}
	}
}

func (d *Device) Send(ctx context.Context, payload []byte) error {
	if !d.txAllowed.Load() {
		return ErrTxProhibited
	}
	params := []string{
		"1",
		d.getIP(),
		fmt.Sprintf("%04X", echonetPort),
		"1",
		fmt.Sprintf("%04X", len(payload)),
	}
	_, err := d.sendUDP(ctx, params, payload)
	return err
}

func (d *Device) Recv(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-d.closed:
		return nil, io.EOF
	case b := <-d.rxudp:
		return b, nil
	}
}

func (d *Device) Close() error {
	d.closeOnce.Do(func() {
		close(d.closed)
		d.cancel()
		_ = d.port.Close()
	})
	return nil
}
