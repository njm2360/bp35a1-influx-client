package bp35a1

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	cmdSKSREG    = "SKSREG"    // 3.1
	cmdSKSCAN    = "SKSCAN"    // 3.9
	cmdSKLL64    = "SKLL64"    // 3.29
	cmdSKJOIN    = "SKJOIN"    // 3.4
	cmdSKSENDTO  = "SKSENDTO"  // 3.7
	cmdSKSETRBID = "SKSETRBID" // 3.17
	cmdSKSETPWD  = "SKSETPWD"  // 3.16
	cmdSKVER     = "SKVER"     // 3.23
	cmdSKRESET   = "SKRESET"   // 3.25
	cmdWOPT      = "WOPT"      // 3.30
	cmdROPT      = "ROPT"      // 3.31
	cmdWUART     = "WUART"     // 3.32
	cmdRUART     = "RUART"     // 3.33
)

const (
	crlf = "\r\n"
	cr   = "\r"
)

const (
	evNSReceived     = 0x01
	evNAReceived     = 0x02
	evEchoRequest    = 0x05
	evEDScanDone     = 0x1F
	evBeaconReceived = 0x20
	evUDPSendDone    = 0x21
	evActiveScanOK   = 0x22
	evPANAConnectErr = 0x24
	evPANAConnectOK  = 0x25
	evSessionEnd     = 0x26
	evSessionEndOK   = 0x27
	evSessionEndTO   = 0x28
	evLifetimeExpire = 0x29
	evTxLimitOn      = 0x32
	evTxLimitOff     = 0x33
)

var eventName = map[int]string{
	evNSReceived:     "NS received",
	evNAReceived:     "NA received",
	evEchoRequest:    "Echo Request received",
	evEDScanDone:     "ED scan completed",
	evBeaconReceived: "Beacon received",
	evUDPSendDone:    "UDP send completed",
	evActiveScanOK:   "active scan completed",
	evPANAConnectErr: "PANA connection failed",
	evPANAConnectOK:  "PANA connection established",
	evSessionEnd:     "session end requested by peer",
	evSessionEndOK:   "session end succeeded",
	evSessionEndTO:   "session end timed out",
	evLifetimeExpire: "session lifetime expired",
	evTxLimitOn:      "ARIB transmit-time limit engaged",
	evTxLimitOff:     "ARIB transmit-time limit released",
}

var (
	ErrTxProhibited = errors.New("bp35a1: UDP transmit prohibited (no PANA session)")
	ErrTxLimited    = errors.New("bp35a1: UDP transmit blocked (ARIB transmit-time limit)")
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

func (s rxState) String() string {
	switch s {
	case stateNormal:
		return "normal"
	case stateEpandesc:
		return "epandesc"
	case stateSKLL64:
		return "skll64"
	case stateProductRead:
		return "product_read"
	default:
		return "unknown"
	}
}

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

	reconnectCh chan struct{}

	epan     Epan
	epanSeen map[string]bool

	baud      int
	routeBID  string
	password  string
	epanCache string

	sessionEst atomic.Bool
	txAllowed  atomic.Bool
	ip         atomic.Value // string

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
		port:        port,
		log:         opts.Logger,
		ctx:         dctx,
		cancel:      cancel,
		newline:     crlf,
		state:       stateNormal,
		baud:        baud,
		routeBID:    opts.RouteBID,
		password:    opts.Password,
		epanCache:   opts.EpanCache,
		results:     make(chan string, 8),
		responses:   make(chan string, 32),
		events:      make(chan skEvent, 16),
		epans:       make(chan Epan, 4),
		rxudp:       make(chan []byte, 8),
		reconnectCh: make(chan struct{}, 1),
		closed:      make(chan struct{}),
	}
	d.reconnect = d.reestablish
	d.txAllowed.Store(true)

	go d.readLoop()

	if err := d.setup(ctx); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *Device) Send(ctx context.Context, payload []byte) error {
	if !d.sessionEst.Load() {
		return ErrTxProhibited
	}
	if !d.txAllowed.Load() {
		return ErrTxLimited
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

func (d *Device) Reconnect() {
	d.sessionEst.Store(false)
	d.signalReconnect()
}

func (d *Device) Close() error {
	d.closeOnce.Do(func() {
		close(d.closed)
		d.cancel()
		_ = d.port.Close()
	})
	return nil
}
