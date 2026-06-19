package bp35a1

import (
	"context"
	"strings"
	"testing"
	"time"
)

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

func TestReestablishAbortsWhenCtxDone(t *testing.T) {
	d := newTestDevice()
	d.cancel() // ctx 終了済み
	// connect/scan(ポート操作)に到達せず即 ok=false で返ること。
	if _, ok := d.reestablish(Epan{Channel: 0x21}); ok {
		t.Fatal("reestablish should not succeed after ctx cancel")
	}
}
