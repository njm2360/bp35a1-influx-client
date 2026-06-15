package bp35a1

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func newTestDevice() *Device {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Device{
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		ctx:       ctx,
		cancel:    cancel,
		newline:   "\r\n",
		state:     stateNormal,
		results:   make(chan string, 8),
		responses: make(chan string, 32),
		events:    make(chan skEvent, 16),
		epans:     make(chan Epan, 4),
		rxudp:     make(chan []byte, 8),
		closed:    make(chan struct{}),
	}
	d.txAllowed.Store(true)
	return d
}

func TestFeedERXUDP(t *testing.T) {
	d := newTestDevice()
	// dst port 0E1A = 3610(ECHONET)。data = 1081... の ECHONET フレーム。
	line := "ERXUDP FE80::1 FE80::2 0E1A 0E1A 001D129012345678 1 000A 1081000102880105FF01\r\n"
	d.feed([]byte(line))

	select {
	case b := <-d.rxudp:
		if len(b) != 10 || b[0] != 0x10 || b[1] != 0x81 {
			t.Fatalf("unexpected payload: %x", b)
		}
	case <-time.After(time.Second):
		t.Fatal("no rxudp payload")
	}
}

func TestFeedERXUDPNonEchonetIgnored(t *testing.T) {
	d := newTestDevice()
	line := "ERXUDP FE80::1 FE80::2 0E1A 1234 001D129012345678 1 0002 ABCD\r\n"
	d.feed([]byte(line))
	select {
	case b := <-d.rxudp:
		t.Fatalf("non-ECHONET port should be ignored, got %x", b)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFeedEvent(t *testing.T) {
	d := newTestDevice()
	d.feed([]byte("EVENT 25 FE80::2\r\n")) // PANA_CONNECT_OK
	if !d.sessionEst.Load() {
		t.Fatal("sessionEst should be set on PANA connect OK")
	}
	select {
	case ev := <-d.events:
		if ev.code != evPANAConnectOK {
			t.Fatalf("want PANA_CONNECT_OK, got %#x", ev.code)
		}
	case <-time.After(time.Second):
		t.Fatal("no event delivered")
	}
}

func TestManageReconnectsOnLifetimeExpire(t *testing.T) {
	d := newTestDevice()
	defer d.cancel()

	called := make(chan Epan, 1)
	d.reconnect = func(e Epan) (Epan, bool) {
		called <- e
		return e, true
	}
	// セッション確立済みを模しておく。
	d.sessionEst.Store(true)
	go d.manage(Epan{Channel: 0x21})

	// EVENT 29 = PANA セッション期限切れ。
	d.feed([]byte("EVENT 29 FE80::2\r\n"))

	select {
	case e := <-called:
		if e.Channel != 0x21 {
			t.Fatalf("reconnect got unexpected epan: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnect was not triggered on EVENT 29")
	}
	if d.sessionEst.Load() {
		t.Fatal("sessionEst should be cleared on session expiry")
	}
}

func TestFeedEpandescChunked(t *testing.T) {
	d := newTestDevice()
	// 複数行を分割して投入し、行境界をまたいでも組み立てられること。
	d.feed([]byte("EPANDESC\r\n  Channel:21\r\n  Channel Page:09\r\n"))
	d.feed([]byte("  Pan ID:8888\r\n  Addr:001D1290"))
	d.feed([]byte("12345678\r\n  LQI:E1\r\n  PairID:00112233\r\n"))

	select {
	case e := <-d.epans:
		if e.Channel != 0x21 || e.PanID != 0x8888 || e.LQI != 0xE1 {
			t.Fatalf("unexpected epan: %+v", e)
		}
		if e.MACAddress != "001D129012345678" || e.PairID != "00112233" {
			t.Fatalf("unexpected epan strings: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("EPANDESC not assembled")
	}
	if d.currentState() != stateNormal {
		t.Fatal("state should return to NORMAL after EPANDESC")
	}
}

func TestFeedOKResult(t *testing.T) {
	d := newTestDevice()
	d.feed([]byte("OK\r\n"))
	select {
	case r := <-d.results:
		if r != "OK" {
			t.Fatalf("want OK, got %q", r)
		}
	case <-time.After(time.Second):
		t.Fatal("no result")
	}
}

func TestProductReadState(t *testing.T) {
	// ROPT は CR 終端で "OK 01"(結果 + 値)を 1 行で返す。
	d := newTestDevice()
	d.setMode("\r", stateProductRead)
	d.feed([]byte("OK 01\r"))

	select {
	case v := <-d.responses:
		if v != "01" {
			t.Fatalf("want response 01, got %q", v)
		}
	case <-time.After(time.Second):
		t.Fatal("no response")
	}
	select {
	case r := <-d.results:
		if r != "OK" {
			t.Fatalf("want OK, got %q", r)
		}
	case <-time.After(time.Second):
		t.Fatal("no result")
	}
}

func TestHexToBytes(t *testing.T) {
	b, err := hexToBytes("1081ABCD")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x10, 0x81, 0xAB, 0xCD}
	for i := range want {
		if b[i] != want[i] {
			t.Fatalf("byte %d: got %#x want %#x", i, b[i], want[i])
		}
	}
	if _, err := hexToBytes("ZZ"); err == nil {
		t.Fatal("invalid hex should error")
	}
}

func TestCommandError(t *testing.T) {
	err := commandError("FAIL ER06")
	if err == nil {
		t.Fatal("expected error")
	}
}
