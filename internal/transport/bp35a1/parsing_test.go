package bp35a1

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHandleEventLifetimeExpireClearsTx(t *testing.T) {
	d := newTestDevice()
	d.txAllowed.Store(true)
	d.feed([]byte("EVENT 29 FE80::2\r\n")) // セッション期限切れ
	if d.txAllowed.Load() {
		t.Fatal("txAllowed should be cleared on EVENT 29")
	}
	select {
	case ev := <-d.events:
		if ev.code != evLifetimeExpire {
			t.Fatalf("want lifetime-expire event, got %#x", ev.code)
		}
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}
}

func TestHandleEventMalformedIgnored(t *testing.T) {
	d := newTestDevice()
	d.txAllowed.Store(false)
	d.feed([]byte("EVENT 25\r\n"))         // フィールド不足(<3)
	d.feed([]byte("EVENT ZZ FE80::2\r\n")) // コードが不正な16進
	if d.txAllowed.Load() {
		t.Fatal("malformed EVENT must not set txAllowed")
	}
	if len(d.events) != 0 {
		t.Fatalf("no event should be queued, got %d", len(d.events))
	}
}

func TestHandleERXUDPMalformedIgnored(t *testing.T) {
	d := newTestDevice()
	d.feed([]byte("ERXUDP too few fields\r\n"))                // フィールド不足
	d.feed([]byte("ERXUDP s d 0E1A 0E1A lla 1 0002 ZZZZ\r\n")) // ペイロードが不正な16進
	select {
	case b := <-d.rxudp:
		t.Fatalf("malformed ERXUDP should be ignored, got %x", b)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestProcessNormalFail(t *testing.T) {
	d := newTestDevice()
	d.feed([]byte("FAIL ER06\r\n"))
	select {
	case r := <-d.results:
		if r != "FAIL ER06" {
			t.Fatalf("want FAIL ER06, got %q", r)
		}
	case <-time.After(time.Second):
		t.Fatal("FAIL not routed to results")
	}
}

func TestEpandescPartialNotEmitted(t *testing.T) {
	d := newTestDevice()
	// 6項目のうち一部のみ。完成しないので emit されず、状態は epandesc のまま。
	d.feed([]byte("EPANDESC\r\n  Channel:21\r\n  Pan ID:8888\r\n"))
	if len(d.epans) != 0 {
		t.Fatal("incomplete EPANDESC must not be emitted")
	}
	if d.currentState() != stateEpandesc {
		t.Fatal("state should remain epandesc until complete")
	}
}

func TestSKLL64State(t *testing.T) {
	d := newTestDevice()
	d.setMode("\r\n", stateSKLL64)
	d.feed([]byte("FE80:0000:0000:0000:021D:1290:1234:5678\r\n"))

	select {
	case r := <-d.responses:
		if r != "FE80:0000:0000:0000:021D:1290:1234:5678" {
			t.Fatalf("unexpected SKLL64 address: %q", r)
		}
	case <-time.After(time.Second):
		t.Fatal("no SKLL64 response")
	}
	select {
	case r := <-d.results:
		if r != "OK" {
			t.Fatalf("SKLL64 should synthesize OK, got %q", r)
		}
	default:
		t.Fatal("SKLL64 should push OK to results")
	}
	if d.currentState() != stateNormal {
		t.Fatal("state should return to normal after SKLL64")
	}
}

func TestSKLL64Fail(t *testing.T) {
	d := newTestDevice()
	d.setMode("\r\n", stateSKLL64)
	d.feed([]byte("FAIL ER06\r\n"))
	select {
	case r := <-d.results:
		if r != "FAIL ER06" {
			t.Fatalf("want FAIL ER06, got %q", r)
		}
	default:
		t.Fatal("SKLL64 FAIL should go to results")
	}
	if len(d.responses) != 0 {
		t.Fatal("FAIL should not produce a response line")
	}
}

func TestParseHex(t *testing.T) {
	cases := map[string]int{"21": 0x21, " FF ": 0xFF, "8888": 0x8888, "ZZ": 0, "": 0}
	for in, want := range cases {
		if got := parseHex(in); got != want {
			t.Errorf("parseHex(%q)=%d want %d", in, got, want)
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

func TestEpanSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "epan.json")
	in := Epan{Channel: 0x21, ChannelPage: 0x09, PanID: 0x8888, MACAddress: "001D129012345678", LQI: 0xE1, PairID: "00112233"}
	if err := saveEpan(path, in); err != nil {
		t.Fatalf("saveEpan: %v", err)
	}
	got, ok := loadEpan(path)
	if !ok {
		t.Fatal("loadEpan returned ok=false")
	}
	if got != in {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", got, in)
	}
}

func TestLoadEpanRejects(t *testing.T) {
	dir := t.TempDir()

	if _, ok := loadEpan(""); ok {
		t.Error("empty path should fail")
	}
	if _, ok := loadEpan(filepath.Join(dir, "missing.json")); ok {
		t.Error("missing file should fail")
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadEpan(bad); ok {
		t.Error("invalid JSON should fail")
	}

	noMAC := filepath.Join(dir, "nomac.json")
	if err := os.WriteFile(noMAC, []byte(`{"channel":33}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadEpan(noMAC); ok {
		t.Error("missing MACAddress should fail")
	}
}

func TestSaveEpanEmptyPathNoop(t *testing.T) {
	if err := saveEpan("", Epan{MACAddress: "x"}); err != nil {
		t.Fatalf("saveEpan with empty path should be a no-op, got %v", err)
	}
}
