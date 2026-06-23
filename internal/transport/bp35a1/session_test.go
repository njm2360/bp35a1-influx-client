package bp35a1

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const epandescResponse = "OK\r\nEPANDESC\r\n  Channel:21\r\n  Channel Page:09\r\n" +
	"  Pan ID:8888\r\n  Addr:001D129012345678\r\n  LQI:E1\r\n  PairID:00112233\r\n" +
	"EVENT 22 FE80::1\r\n"

func meterResponder(fp *fakePort, mac string, panaEvent string) func([]byte) {
	return func(p []byte) {
		s := string(p)
		switch {
		case strings.HasPrefix(s, "SKLL64"):
			fp.push([]byte(mac + "\r\n"))
		case strings.HasPrefix(s, "SKJOIN"):
			fp.push([]byte("OK\r\n" + panaEvent + "\r\n"))
		default:
			fp.push([]byte("OK\r\n"))
		}
	}
}

func TestGetVersions(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func(p []byte) {
		switch {
		case strings.HasPrefix(string(p), "SKVER"):
			fp.push([]byte("EVER 1.2.3\r\nOK\r\n"))
		case strings.HasPrefix(string(p), "SKAPPVER"):
			fp.push([]byte("EAPPVER rev26e\r\nOK\r\n"))
		default:
			fp.push([]byte("OK\r\n"))
		}
	}
	d := newDeviceWithPort(fp)
	defer d.Close()

	stack, app, err := d.getVersions(context.Background())
	if err != nil {
		t.Fatalf("getVersions: %v", err)
	}
	// EVER / EAPPVER のプレフィックスが除去され値だけ残ること。
	if stack != "1.2.3" {
		t.Fatalf("stack = %q, want 1.2.3", stack)
	}
	if app != "rev26e" {
		t.Fatalf("app = %q, want rev26e", app)
	}
}

func TestCorrectBaudrateDetects(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func(p []byte) {
		if strings.HasPrefix(strings.TrimSpace(string(p)), "SKVER") {
			fp.push([]byte("EVER 1.2.3\r\nOK\r\n"))
		}
	}
	d := newDeviceWithPort(fp)
	defer d.Close()

	if err := d.correctBaudrate(context.Background(), defaultBaud); err != nil {
		t.Fatalf("correctBaudrate: %v", err)
	}
}

func TestCorrectBaudrateFailsWhenNoEVER(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func(p []byte) {
		// どのボーレートでも EVER を返さない(FAIL のみ)。
		if strings.HasPrefix(strings.TrimSpace(string(p)), "SKVER") {
			fp.push([]byte("FAIL ER\r\n"))
		}
	}
	d := newDeviceWithPort(fp)
	defer d.Close()

	if err := d.correctBaudrate(context.Background(), defaultBaud); err == nil {
		t.Fatal("expected error when no baudrate yields EVER")
	}
}

// TestInitModule はモジュール初期化ハンドシェイク全体を検証する。
// 実機のエコーバック(echo フラグ ON 中)を再現しつつ、ROPT の値によって
// WOPT 設定が出る/出ないことと、最終的に echo が無効化されることを確認する。
func TestInitModule(t *testing.T) {
	cases := []struct {
		name     string
		optResp  string // ROPT が返すオプション値
		wantWOPT bool   // WOPT 書き込みを期待するか
	}{
		{"opt_already_set_skips_wopt", "01", false}, // 設定済みなら WOPT 省略
		{"opt_unset_writes_wopt", "00", true},       // 未設定なら WOPT で設定
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fp := newFakePort()
			d := newDeviceWithPort(fp)
			defer d.Close()

			optResp := tc.optResp
			fp.onWrite = func(p []byte) {
				s := strings.TrimSpace(string(p))
				if s == "" {
					return // ボーレート検出時の CRLF プローブ
				}
				// 実機は常にエコーバックする。ドライバの echo フラグが
				// 立っている間(SFE 0 で無効化するまで)だけ再現する。
				if d.echo.Load() {
					fp.push([]byte(s + "\r\n"))
				}
				switch {
				case strings.HasPrefix(s, "SKVER"):
					fp.push([]byte("EVER 1.2.3\r\nOK\r\n"))
				case strings.HasPrefix(s, "ROPT"):
					fp.push([]byte("OK " + optResp + "\r")) // CR 終端・OK+値
				case strings.HasPrefix(s, "WOPT"):
					fp.push([]byte("OK\r")) // CR 終端
				default:
					fp.push([]byte("OK\r\n"))
				}
			}

			if err := d.initModule(context.Background()); err != nil {
				t.Fatalf("initModule: %v", err)
			}
			if d.echo.Load() {
				t.Fatal("echo should be disabled after SFE 0")
			}
			if got := strings.Contains(fp.writtenString(), "WOPT"); got != tc.wantWOPT {
				t.Fatalf("WOPT written = %v, want %v", got, tc.wantWOPT)
			}
		})
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
			fp.push([]byte(epandescResponse))
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

func TestEstablishUsesCachedEpan(t *testing.T) {
	const ip = "FE80::5"
	cache := filepath.Join(t.TempDir(), "epan.json")
	if err := saveEpan(cache, Epan{Channel: 0x21, PanID: 0x8888, MACAddress: "001D129012345678"}); err != nil {
		t.Fatalf("saveEpan: %v", err)
	}

	fp := newFakePort()
	var mu sync.Mutex
	var scans int
	fp.onWrite = func(p []byte) {
		s := string(p)
		if strings.HasPrefix(s, "SKSCAN") {
			mu.Lock()
			scans++
			mu.Unlock()
		}
		meterResponder(fp, ip, "EVENT 25 "+ip)(p)
	}
	d := newDeviceWithPort(fp)
	d.epanCache = cache
	defer d.Close()

	if _, err := d.establish(context.Background()); err != nil {
		t.Fatalf("establish: %v", err)
	}
	if d.getIP() != ip || !d.sessionEst.Load() {
		t.Fatalf("session not established: ip=%q est=%v", d.getIP(), d.sessionEst.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if scans != 0 {
		t.Fatalf("cached path should not scan, got %d scans", scans)
	}
}

func TestEstablishRescansWhenCachedConnectFails(t *testing.T) {
	const ip = "FE80::6"
	cache := filepath.Join(t.TempDir(), "epan.json")
	if err := saveEpan(cache, Epan{Channel: 0x07, PanID: 0x1111, MACAddress: "001D129012345678"}); err != nil {
		t.Fatalf("saveEpan: %v", err)
	}

	fp := newFakePort()
	var mu sync.Mutex
	var joins int
	fp.onWrite = func(p []byte) {
		s := string(p)
		switch {
		case strings.HasPrefix(s, "SKSCAN"):
			fp.push([]byte(epandescResponse))
		case strings.HasPrefix(s, "SKLL64"):
			fp.push([]byte(ip + "\r\n"))
		case strings.HasPrefix(s, "SKJOIN"):
			mu.Lock()
			joins++
			n := joins
			mu.Unlock()
			if n == 1 {
				fp.push([]byte("OK\r\nEVENT 24 " + ip + "\r\n")) // 初回(古いキャッシュ)は失敗
			} else {
				fp.push([]byte("OK\r\nEVENT 25 " + ip + "\r\n")) // 再スキャン後は成功
			}
		default:
			fp.push([]byte("OK\r\n"))
		}
	}
	d := newDeviceWithPort(fp)
	d.epanCache = cache
	defer d.Close()

	epan, err := d.establish(context.Background())
	if err != nil {
		t.Fatalf("establish: %v", err)
	}
	if d.getIP() != ip || !d.sessionEst.Load() {
		t.Fatalf("session not established: ip=%q est=%v", d.getIP(), d.sessionEst.Load())
	}
	// 再スキャンで取得したEPANに更新されていること。
	if epan.Channel != 0x21 {
		t.Fatalf("epan should be refreshed by rescan, got channel %#x", epan.Channel)
	}
	mu.Lock()
	defer mu.Unlock()
	if joins != 2 {
		t.Fatalf("want exactly 2 JOIN attempts (initial + retry), got %d", joins)
	}
}

func TestRecoveryLadder(t *testing.T) {
	cases := []struct {
		failures int
		want     recoveryStep
	}{
		{0, stepNone}, // 初回は何もしない
		{1, stepNone},
		{4, stepNone},
		{5, stepRescan},  // rescanAfterFailures ごとに再スキャン
		{10, stepRescan}, // reset の倍数でなければ rescan のまま
		{15, stepRescan},
		{20, stepReset}, // resetAfterFailures ごとにモジュールリセット(rescan を兼ねる)
		{25, stepRescan},
		{40, stepReset},
	}
	for _, tc := range cases {
		if got := recoveryFor(tc.failures); got != tc.want {
			t.Errorf("recoveryFor(%d) = %d, want %d", tc.failures, got, tc.want)
		}
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
