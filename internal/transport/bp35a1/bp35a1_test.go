package bp35a1

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bug.st/serial"
)

func newTestDevice() *Device {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Device{
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		ctx:         ctx,
		cancel:      cancel,
		newline:     "\r\n",
		state:       stateNormal,
		results:     make(chan string, 8),
		responses:   make(chan string, 32),
		events:      make(chan skEvent, 16),
		epans:       make(chan Epan, 4),
		rxudp:       make(chan []byte, 8),
		reconnectCh: make(chan struct{}, 1),
		closed:      make(chan struct{}),
	}
	d.txAllowed.Store(true)
	return d
}

// fakePort は serial.Port を満たすインメモリの疑似ポート。
// Write された内容を記録し、onWrite で任意の応答を readCh へ流せる。
type fakePort struct {
	mu        sync.Mutex
	written   bytes.Buffer
	readCh    chan []byte
	rem       []byte
	closeOnce sync.Once
	closed    chan struct{}
	onWrite   func(p []byte) // Write 時に呼ばれる(疑似デバイスの応答生成)
}

func newFakePort() *fakePort {
	return &fakePort{
		readCh: make(chan []byte, 16),
		closed: make(chan struct{}),
	}
}

func (f *fakePort) push(b []byte) {
	select {
	case f.readCh <- b:
	case <-f.closed:
	}
}

func (f *fakePort) writtenString() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.written.String()
}

func (f *fakePort) Read(p []byte) (int, error) {
	for len(f.rem) == 0 {
		select {
		case <-f.closed:
			return 0, io.EOF
		case b := <-f.readCh:
			f.rem = b
		}
	}
	n := copy(p, f.rem)
	f.rem = f.rem[n:]
	return n, nil
}

func (f *fakePort) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.written.Write(p)
	f.mu.Unlock()
	if f.onWrite != nil {
		f.onWrite(p)
	}
	return len(p), nil
}

func (f *fakePort) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func (f *fakePort) SetMode(*serial.Mode) error { return nil }
func (f *fakePort) Drain() error               { return nil }
func (f *fakePort) ResetInputBuffer() error    { return nil }
func (f *fakePort) ResetOutputBuffer() error   { return nil }
func (f *fakePort) SetDTR(bool) error          { return nil }
func (f *fakePort) SetRTS(bool) error          { return nil }
func (f *fakePort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return &serial.ModemStatusBits{}, nil
}
func (f *fakePort) SetReadTimeout(time.Duration) error { return nil }
func (f *fakePort) Break(time.Duration) error          { return nil }

var _ serial.Port = (*fakePort)(nil)

// newDeviceWithPort は fake port を繋いだ Device を返し、readLoop を起動する。
func newDeviceWithPort(p serial.Port) *Device {
	d := newTestDevice()
	d.port = p
	go d.readLoop()
	return d
}

func TestSendBuildsSKSENDTO(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func([]byte) { fp.push([]byte("OK\r\n")) }
	d := newDeviceWithPort(fp)
	defer d.Close()
	d.sessionEst.Store(true)
	d.setIP("FE80::1")

	if err := d.Send(context.Background(), []byte{0x10, 0x81}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	w := fp.writtenString()
	// SKSENDTO <handle> <ip> <port4hex> <sec> <len4hex> <data>
	if !strings.HasPrefix(w, "SKSENDTO 1 FE80::1 0E1A 1 0002 ") {
		t.Fatalf("unexpected SKSENDTO header: %q", w)
	}
	if !strings.HasSuffix(w, "\x10\x81") {
		t.Fatalf("payload bytes not appended verbatim: %q", w)
	}
}

func TestSendProhibitedWithoutSession(t *testing.T) {
	fp := newFakePort()
	d := newDeviceWithPort(fp)
	defer d.Close()
	// sessionEst=false(既定)。送信は即拒否され、ポートには何も書かれない。
	if err := d.Send(context.Background(), []byte{0x10, 0x81}); err != ErrTxProhibited {
		t.Fatalf("want ErrTxProhibited, got %v", err)
	}
	if fp.writtenString() != "" {
		t.Fatalf("nothing should be written, got %q", fp.writtenString())
	}
}

func TestSendBlockedByTxLimit(t *testing.T) {
	fp := newFakePort()
	d := newDeviceWithPort(fp)
	defer d.Close()
	// セッションは確立済みだが ARIB 送信総和時間制限が発動中(EVENT 0x32 相当)。
	d.sessionEst.Store(true)
	d.txAllowed.Store(false)
	if err := d.Send(context.Background(), []byte{0x10, 0x81}); err != ErrTxLimited {
		t.Fatalf("want ErrTxLimited, got %v", err)
	}
	if fp.writtenString() != "" {
		t.Fatalf("nothing should be written, got %q", fp.writtenString())
	}
}

func TestSendRejectsPayloadSize(t *testing.T) {
	cases := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{"empty", 0, true},
		{"min", 1, false},
		{"max", maxUDPPayload, false},
		{"over", maxUDPPayload + 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fp := newFakePort()
			fp.onWrite = func([]byte) { fp.push([]byte("OK\r\n")) }
			d := newDeviceWithPort(fp)
			defer d.Close()
			d.sessionEst.Store(true)
			d.setIP("FE80::1")

			err := d.Send(context.Background(), make([]byte, tc.size))
			if tc.wantErr {
				if !errors.Is(err, ErrPayloadSize) {
					t.Fatalf("want ErrPayloadSize, got %v", err)
				}
				if fp.writtenString() != "" {
					t.Fatalf("nothing should be written, got %q", fp.writtenString())
				}
				return
			}
			if err != nil {
				t.Fatalf("Send(%d bytes): %v", tc.size, err)
			}
		})
	}
}

func TestRecv(t *testing.T) {
	d := newTestDevice()

	d.rxudp <- []byte{0x10, 0x81}
	b, err := d.Recv(context.Background())
	if err != nil || len(b) != 2 || b[0] != 0x10 {
		t.Fatalf("Recv = %x, %v", b, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Recv(ctx); err == nil {
		t.Fatal("Recv should return ctx error when canceled")
	}

	close(d.closed)
	if _, err := d.Recv(context.Background()); err != io.EOF {
		t.Fatalf("Recv after close want io.EOF, got %v", err)
	}
}
