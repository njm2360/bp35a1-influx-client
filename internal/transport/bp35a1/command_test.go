package bp35a1

import (
	"context"
	"strings"
	"testing"
	"time"
)

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

func TestCommandError(t *testing.T) {
	err := commandError("FAIL ER06")
	if err == nil {
		t.Fatal("expected error")
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
