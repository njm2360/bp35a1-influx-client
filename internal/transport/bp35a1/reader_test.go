package bp35a1

import (
	"testing"
	"time"
)

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

func TestFeedBufferOverflowDiscarded(t *testing.T) {
	d := newTestDevice()
	// 改行を含まないゴミが maxLineBytes を超えても、バッファは破棄され肥大化しない。
	garbage := make([]byte, maxLineBytes+1024)
	for i := range garbage {
		garbage[i] = 'x'
	}
	d.feed(garbage)
	if len(d.buf) != 0 {
		t.Fatalf("overflowed buffer should be discarded, got %d bytes", len(d.buf))
	}
	// 破棄後も後続の正常行を処理できること。
	d.feed([]byte("OK\r\n"))
	select {
	case r := <-d.results:
		if r != "OK" {
			t.Fatalf("want OK after overflow reset, got %q", r)
		}
	case <-time.After(time.Second):
		t.Fatal("parser should recover after overflow discard")
	}
}

func TestHandleEventLifetimeExpireClearsTx(t *testing.T) {
	d := newTestDevice()
	d.sessionEst.Store(true)
	d.feed([]byte("EVENT 29 FE80::2\r\n")) // セッション期限切れ
	if d.sessionEst.Load() {
		t.Fatal("sessionEst should be cleared on EVENT 29")
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

func TestHandleEventLifetimeExpireSignalsReconnectEvenWhenEventsFull(t *testing.T) {
	d := newTestDevice()
	d.sessionEst.Store(true)
	// eventsチャネルを満杯にする
	for i := 0; i < cap(d.events); i++ {
		d.events <- skEvent{code: evUDPSendDone}
	}
	d.feed([]byte("EVENT 29 FE80::2\r\n")) // セッション期限切れ

	if d.sessionEst.Load() {
		t.Fatal("sessionEst should be cleared on EVENT 29")
	}
	// events経由ではなくreconnectChで再接続が伝わること
	select {
	case <-d.reconnectCh:
	default:
		t.Fatal("reconnect must be signaled even when events channel is full")
	}
}

func TestHandleEventSessionEndTriggersReconnect(t *testing.T) {
	// 0x26(相手都合の終了要求)/0x27(終了成功)/0x28(終了タイムアウト)/0x29(期限切れ)
	// はいずれもセッション喪失となるためsessionEstをクリアし再接続を促す
	for _, ev := range []string{"26", "27", "28", "29"} {
		t.Run("EVENT_"+ev, func(t *testing.T) {
			d := newTestDevice()
			d.sessionEst.Store(true)
			d.feed([]byte("EVENT " + ev + " FE80::2\r\n"))

			if d.sessionEst.Load() {
				t.Fatalf("EVENT %s should clear sessionEst", ev)
			}
			select {
			case <-d.reconnectCh:
			default:
				t.Fatalf("EVENT %s should signal reconnect", ev)
			}
		})
	}
}

func TestHandleEventTxLimit(t *testing.T) {
	d := newTestDevice()                   // txAllowed=true(既定)
	d.feed([]byte("EVENT 32 FE80::2\r\n")) // ARIB 送信総和時間制限 発動
	if d.txAllowed.Load() {
		t.Fatal("txAllowed should be cleared on EVENT 32")
	}
	d.feed([]byte("EVENT 33 FE80::2\r\n")) // 制限解除
	if !d.txAllowed.Load() {
		t.Fatal("txAllowed should be restored on EVENT 33")
	}
}

func TestHandleEventMalformedIgnored(t *testing.T) {
	d := newTestDevice()
	d.sessionEst.Store(false)
	d.feed([]byte("EVENT 25\r\n"))         // フィールド不足(<3)
	d.feed([]byte("EVENT ZZ FE80::2\r\n")) // コードが不正な16進
	if d.sessionEst.Load() {
		t.Fatal("malformed EVENT must not set sessionEst")
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

func TestEpandescResetOnUnexpectedLine(t *testing.T) {
	d := newTestDevice()
	d.setState(stateEpandesc)
	// コロンを含まない想定外の行 → パーサをリセットし通常処理にフォールバック。
	d.feed([]byte("UNEXPECTED\r\n"))
	if d.currentState() != stateNormal {
		t.Fatalf("state should reset to normal, got %v", d.currentState())
	}
	select {
	case r := <-d.responses:
		if r != "UNEXPECTED" {
			t.Fatalf("want fallback line on responses, got %q", r)
		}
	default:
		t.Fatal("unexpected line should be reprocessed as normal")
	}
}

func TestClearBuffer(t *testing.T) {
	d := newTestDevice()
	d.feed([]byte("partial-without-newline")) // 改行なし → バッファに滞留
	if len(d.buf) == 0 {
		t.Fatal("buffer should hold partial line")
	}
	d.clearBuffer()
	if len(d.buf) != 0 {
		t.Fatalf("clearBuffer should empty the buffer, got %d bytes", len(d.buf))
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
