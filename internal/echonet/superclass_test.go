package echonet

import (
	"testing"
	"time"
)

func TestDecodeOperationStatus(t *testing.T) {
	if on, err := DecodeOperationStatus([]byte{0x30}); err != nil || !on {
		t.Fatalf("0x30 want on, got on=%v err=%v", on, err)
	}
	if on, err := DecodeOperationStatus([]byte{0x31}); err != nil || on {
		t.Fatalf("0x31 want off, got on=%v err=%v", on, err)
	}
	if _, err := DecodeOperationStatus([]byte{0x00}); err == nil {
		t.Fatal("invalid code should error")
	}
	if _, err := DecodeOperationStatus(nil); err == nil {
		t.Fatal("empty should error")
	}
}

func TestDecodeFaultStatus(t *testing.T) {
	if f, err := DecodeFaultStatus([]byte{0x41}); err != nil || !f {
		t.Fatalf("0x41 want fault, got %v err=%v", f, err)
	}
	if f, err := DecodeFaultStatus([]byte{0x42}); err != nil || f {
		t.Fatalf("0x42 want no fault, got %v err=%v", f, err)
	}
	if _, err := DecodeFaultStatus([]byte{0x40}); err == nil {
		t.Fatal("invalid code should error")
	}
}

func TestDecodeCurrentTimeAndDate(t *testing.T) {
	h, m, err := DecodeCurrentTime([]byte{0x17, 0x3B})
	if err != nil || h != 23 || m != 59 {
		t.Fatalf("time want 23:59, got %d:%d err=%v", h, m, err)
	}
	y, mo, d, err := DecodeCurrentDate([]byte{0x07, 0xEA, 0x06, 0x0F})
	if err != nil || y != 2026 || mo != time.June || d != 15 {
		t.Fatalf("date want 2026-06-15, got %d-%d-%d err=%v", y, mo, d, err)
	}
	if _, _, err := DecodeCurrentTime([]byte{0x00}); err == nil {
		t.Fatal("short time should error")
	}
}

func TestDecodePropertyMapDirect(t *testing.T) {
	// プロパティ数<16: 直接列挙
	got, err := DecodePropertyMap([]byte{0x03, 0x80, 0xE7, 0xE8})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x80, 0xE7, 0xE8}
	if len(got) != len(want) {
		t.Fatalf("want %x, got %x", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %x, got %x", want, got)
		}
	}
	if _, err := DecodePropertyMap([]byte{0x03, 0x80}); err == nil {
		t.Fatal("truncated direct map should error")
	}
}

func TestDecodePropertyMapBitmap(t *testing.T) {
	// プロパティ数>=16: ビットマップ形式。
	// byte index0(=下位ニブル0) の bit0(=上位ニブル8) を立てる → EPC 0x80。
	edt := make([]byte, 17)
	edt[0] = 16        // n>=16
	edt[1] = 0x01      // i=0, bit0 → 0x80
	edt[1+7] |= 1 << 7 // i=7, bit7 → EPC 0xF7
	got, err := DecodePropertyMap(edt)
	if err != nil {
		t.Fatal(err)
	}
	has := func(e byte) bool {
		for _, g := range got {
			if g == e {
				return true
			}
		}
		return false
	}
	if !has(0x80) || !has(0xF7) {
		t.Fatalf("want 0x80 and 0xF7 present, got %x", got)
	}
	if _, err := DecodePropertyMap([]byte{0x10, 0x00}); err == nil {
		t.Fatal("truncated bitmap should error")
	}
}
