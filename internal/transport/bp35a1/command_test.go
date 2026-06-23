package bp35a1

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCommandReturnsResponse(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func([]byte) { fp.push([]byte("EVER 1.2.3\r\nOK\r\n")) }
	d := newDeviceWithPort(fp)
	defer d.Close()

	res, err := d.command(context.Background(), cmdSKVER, nil, time.Second)
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

// TestExecCommandModes は exec の switch (cmd) 分岐を網羅する。
// コマンド種別ごとに改行コード(CR/CRLF)・パーサ状態・応答解析が
// 切り替わることを、ポートへの書き込みと戻り値の両面で検証する。
func TestExecCommandModes(t *testing.T) {
	cases := []struct {
		name        string
		cmd         string
		params      []string
		reply       string // 疑似デバイスが返す生バイト列
		wantWritten string // ポートへ書かれるべきバイト列
		wantRes     string
		wantNewline string
	}{
		{
			// ROPT は CR 終端・OK 値を 1 行解析
			name:        "ropt_cr_parses_ok_value",
			cmd:         cmdROPT,
			reply:       "OK 01\r",
			wantWritten: "ROPT\r",
			wantRes:     "01",
			wantNewline: cr,
		},
		{
			// cmdRUART が if 条件から漏れていた回帰の永続ガード。RUART も ROPT 同様に値を解析する。
			name:        "ruart_cr_parses_ok_value",
			cmd:         cmdRUART,
			reply:       "OK 02\r",
			wantWritten: "RUART\r",
			wantRes:     "02",
			wantNewline: cr,
		},
		{
			// WOPT は CR 終端・値なし OK
			name:        "wopt_cr_ok_without_value",
			cmd:         cmdWOPT,
			params:      []string{"01"},
			reply:       "OK\r",
			wantWritten: "WOPT 01\r",
			wantRes:     "",
			wantNewline: cr,
		},
		{
			// WUART も CR 終端・値なし OK
			name:        "wuart_cr_ok_without_value",
			cmd:         cmdWUART,
			params:      []string{"05"},
			reply:       "OK\r",
			wantWritten: "WUART 05\r",
			wantRes:     "",
			wantNewline: cr,
		},
		{
			// SKLL64 は CRLF・OK/FAIL なしのアドレスを応答
			name:        "skll64_crlf_address_without_ok",
			cmd:         cmdSKLL64,
			params:      []string{"00112233445566778899AABBCCDDEEFF"},
			reply:       "FE80::1\r\n",
			wantWritten: "SKLL64 00112233445566778899AABBCCDDEEFF\r\n",
			wantRes:     "FE80::1",
			wantNewline: crlf,
		},
		{
			// 既定コマンドは CRLF 終端・通常解析
			name:        "default_command_crlf",
			cmd:         cmdSKVER,
			reply:       "EVER 1.2.3\r\nOK\r\n",
			wantWritten: "SKVER\r\n",
			wantRes:     "EVER 1.2.3",
			wantNewline: crlf,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fp := newFakePort()
			reply := tc.reply
			fp.onWrite = func([]byte) { fp.push([]byte(reply)) }
			d := newDeviceWithPort(fp)
			defer d.Close()

			res, err := d.exec(context.Background(), tc.cmd, tc.params, nil, time.Second)
			if err != nil {
				t.Fatalf("exec: %v", err)
			}
			if res != tc.wantRes {
				t.Fatalf("res = %q, want %q", res, tc.wantRes)
			}
			if got := fp.writtenString(); got != tc.wantWritten {
				t.Fatalf("written = %q, want %q", got, tc.wantWritten)
			}
			if got := d.currentNewline(); got != tc.wantNewline {
				t.Fatalf("newline = %q, want %q", got, tc.wantNewline)
			}
		})
	}
}

// TestExecBinaryPayload は data != nil の分岐を検証する。
// パラメータ列の後ろに空白 1 個を挟んでバイナリを生のまま連結し、
// 末尾に改行を付けないこと(SKSENDTO のフレーム形式)を確認する。
func TestExecBinaryPayload(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func([]byte) { fp.push([]byte("OK\r\n")) }
	d := newDeviceWithPort(fp)
	defer d.Close()

	payload := []byte{0x10, 0x81, 0x00}
	if _, err := d.exec(context.Background(), cmdSKSENDTO,
		[]string{"1", "FE80::1", "0E1A", "1", "0003"}, payload, time.Second); err != nil {
		t.Fatalf("exec: %v", err)
	}
	want := "SKSENDTO 1 FE80::1 0E1A 1 0003 \x10\x81\x00"
	if got := fp.writtenString(); got != want {
		t.Fatalf("written = %q, want %q", got, want)
	}
}

// TestExecContextCancel は select の <-ctx.Done() 分岐を検証する。
func TestExecContextCancel(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func([]byte) {} // 応答しない
	d := newDeviceWithPort(fp)
	defer d.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.exec(ctx, cmdSKVER, nil, nil, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// TestExecClosed は select の <-d.closed 分岐 (ErrClosed) を検証する。
func TestExecClosed(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func([]byte) {} // 応答しない
	d := newDeviceWithPort(fp)
	d.Close() // d.closed をクローズ

	if _, err := d.exec(context.Background(), cmdSKVER, nil, nil, time.Second); err != ErrClosed {
		t.Fatalf("want ErrClosed, got %v", err)
	}
}

func TestCommandFailReturnsError(t *testing.T) {
	fp := newFakePort()
	fp.onWrite = func([]byte) { fp.push([]byte("FAIL ER06\r\n")) }
	d := newDeviceWithPort(fp)
	defer d.Close()

	if _, err := d.command(context.Background(), cmdSKSETRBID, []string{"00112233"}, time.Second); err == nil {
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
	d.echo.Store(true)

	res, err := d.command(context.Background(), cmdSKVER, nil, time.Second)
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

	if _, err := d.command(context.Background(), cmdSKVER, nil, 50*time.Millisecond); err == nil {
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
