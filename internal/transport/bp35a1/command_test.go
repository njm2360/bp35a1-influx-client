package bp35a1

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bug.st/serial"
)

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

func TestCommandReturnsResponse(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func([]byte) { fp.push([]byte("EVER 1.2.3\r\nOK\r\n")) }
	d := newDeviceWithPort(fp)
	defer d.Close()

	res, err := d.command(context.Background(), cmdSKVER, nil, time.Second, false)
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if res != "EVER 1.2.3" {
		t.Fatalf("want response %q, got %q", "EVER 1.2.3", res)
	}
	if !strings.HasPrefix(fp.writtenString(), "SKVER\r\n") {
		t.Fatalf("unexpected written: %q", fp.writtenString())
	}
}

func TestCommandFailReturnsError(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func([]byte) { fp.push([]byte("FAIL ER06\r\n")) }
	d := newDeviceWithPort(fp)
	defer d.Close()

	if _, err := d.command(context.Background(), cmdSKSETRBID, []string{"00112233"}, time.Second, false); err == nil {
		t.Fatal("expected error from FAIL response")
	} else if !strings.Contains(err.Error(), "ER06") {
		t.Fatalf("error should mention ER06: %v", err)
	}
}

func TestCommandSkipsEcho(t *testing.T) {
	fp := newFakePort()
	// 実機はコマンドをエコーバックする。エコー行 + 応答 + OK を返す。
	fp.onWrite = func([]byte) { fp.push([]byte("SKVER\r\nEVER 9.9.9\r\nOK\r\n")) }
	d := newDeviceWithPort(fp)
	defer d.Close()

	res, err := d.command(context.Background(), cmdSKVER, nil, time.Second, true)
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	// エコー(SKVER)は読み飛ばされ、応答のみ残る。
	if res != "EVER 9.9.9" {
		t.Fatalf("echo not skipped, got %q", res)
	}
}

func TestCommandTimeout(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func([]byte) {} // 応答しない
	d := newDeviceWithPort(fp)
	defer d.Close()

	if _, err := d.command(context.Background(), cmdSKVER, nil, 50*time.Millisecond, false); err == nil {
		t.Fatal("expected result timeout")
	}
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

func TestCollectResponses(t *testing.T) {
	d := newTestDevice()
	d.responses <- "line1"
	d.responses <- "line2"
	if got := d.collectResponses(); got != "line1\r\nline2" {
		t.Fatalf("collectResponses = %q", got)
	}
	if got := d.collectResponses(); got != "" {
		t.Fatalf("empty collect should be blank, got %q", got)
	}
}

func TestDrainResponses(t *testing.T) {
	d := newTestDevice()
	d.results <- "OK"
	d.responses <- "stale"
	d.drainResponses()
	if len(d.results) != 0 || len(d.responses) != 0 {
		t.Fatalf("channels not drained: results=%d responses=%d", len(d.results), len(d.responses))
	}
}

func TestSkipEcho(t *testing.T) {
	d := newTestDevice()

	// 一致するエコー行は消費される。
	d.responses <- "SKVER"
	d.skipEcho("SKVER")
	if len(d.responses) != 0 {
		t.Fatalf("matching echo should be consumed, %d left", len(d.responses))
	}

	// 不一致の行は読み戻される。
	d.responses <- "EVER 1.0"
	d.skipEcho("SKVER")
	select {
	case line := <-d.responses:
		if line != "EVER 1.0" {
			t.Fatalf("want put-back EVER 1.0, got %q", line)
		}
	default:
		t.Fatal("non-matching line should be put back")
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

// meterResponder は connect/scan が発行する各コマンドへ疑似メータの応答を返す。
func meterResponder(fp *fakePort, mac string, panaEvent string) func([]byte) {
	return func(p []byte) {
		s := string(p)
		switch {
		case strings.HasPrefix(s, "SKLL64"):
			fp.push([]byte(mac + "\r\n")) // SKLL64 は OK/FAIL なしで IPv6 を返す
		case strings.HasPrefix(s, "SKJOIN"):
			fp.push([]byte("OK\r\n" + panaEvent + "\r\n"))
		default:
			fp.push([]byte("OK\r\n"))
		}
	}
}

func TestConnectHappyPath(t *testing.T) {
	const ip = "FE80:0000:0000:0000:021D:1290:1234:5678"
	fp := newFakePort()
	fp.onWrite = meterResponder(fp, ip, "EVENT 25 "+ip)
	d := newDeviceWithPort(fp)
	defer d.Close()

	got, err := d.connect(context.Background(), Epan{Channel: 0x21, PanID: 0x8888, MACAddress: "001D129012345678"})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if got != ip {
		t.Fatalf("ip want %q, got %q", ip, got)
	}
	if !d.sessionEst.Load() {
		t.Fatal("sessionEst should be set after EVENT 25")
	}
}

func TestConnectPANAFail(t *testing.T) {
	const ip = "FE80::1"
	fp := newFakePort()
	fp.onWrite = meterResponder(fp, ip, "EVENT 24 "+ip) // PANA 接続失敗
	d := newDeviceWithPort(fp)
	defer d.Close()

	if _, err := d.connect(context.Background(), Epan{MACAddress: "001D129012345678"}); err != ErrPANAConnect {
		t.Fatalf("want ErrPANAConnect, got %v", err)
	}
}

func TestScanHappyPath(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func(p []byte) {
		if strings.HasPrefix(string(p), "SKSCAN") {
			// EPANDESC とスキャン完了(EVENT 22)を一気に流す。順序保証により
			// scan は完了を観測しても EPANDESC を取りこぼさない(タイミング非依存)。
			fp.push([]byte("OK\r\nEPANDESC\r\n  Channel:21\r\n  Channel Page:09\r\n" +
				"  Pan ID:8888\r\n  Addr:001D129012345678\r\n  LQI:E1\r\n  PairID:00112233\r\n" +
				"EVENT 22 FE80::1\r\n"))
		}
	}
	d := newDeviceWithPort(fp)
	defer d.Close()

	epan, err := d.scan(context.Background(), 6)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if epan.Channel != 0x21 || epan.PanID != 0x8888 || epan.MACAddress != "001D129012345678" {
		t.Fatalf("unexpected epan: %+v", epan)
	}
}

func TestReestablishReconnects(t *testing.T) {
	const ip = "FE80::9"
	fp := newFakePort()
	fp.onWrite = meterResponder(fp, ip, "EVENT 25 "+ip)
	d := newDeviceWithPort(fp)
	defer d.Close()

	epan, ok := d.reestablish(Epan{Channel: 0x21, MACAddress: "001D129012345678"})
	if !ok {
		t.Fatal("reestablish should succeed")
	}
	if epan.Channel != 0x21 {
		t.Fatalf("epan should be preserved: %+v", epan)
	}
	if d.getIP() != ip || !d.sessionEst.Load() {
		t.Fatalf("session not restored: ip=%q tx=%v", d.getIP(), d.sessionEst.Load())
	}
}

func TestCommandErrorCodes(t *testing.T) {
	cases := map[string]string{
		"FAIL ER04": "ER04",
		"FAIL ER05": "ER05",
		"FAIL ER06": "ER06",
		"FAIL ER09": "ER09",
		"FAIL ER10": "ER10",
		"FAIL ER99": "ER99",
		"FAIL":      "", // コード無し
	}
	for in, code := range cases {
		err := commandError(in)
		if err == nil {
			t.Fatalf("%q: expected error", in)
		}
		if code != "" && !strings.Contains(err.Error(), code) {
			t.Fatalf("%q: error should contain %q: %v", in, code, err)
		}
	}
}
