package echonet

import (
	"testing"
)

func TestDecodeU32(t *testing.T) {
	v, noData, err := DecodeU32([]byte{0x00, 0x01, 0x86, 0xA0})
	if err != nil || noData || v != 100000 {
		t.Fatalf("got v=%d noData=%v err=%v", v, noData, err)
	}
	if _, nd, _ := DecodeU32([]byte{0xFF, 0xFF, 0xFF, 0xFE}); !nd {
		t.Fatal("0xFFFFFFFE (計測データなし) should be noData")
	}
	// 0xFFFFFFFF は本クラスの積算プロパティでは仕様未定義 → 欠測扱いせず値として返す。
	if v, nd, _ := DecodeU32([]byte{0xFF, 0xFF, 0xFF, 0xFF}); nd || v != 0xFFFFFFFF {
		t.Fatalf("0xFFFFFFFF should be a value, got v=%#x noData=%v", v, nd)
	}
	if _, _, err := DecodeU32([]byte{0x01}); err == nil {
		t.Fatal("short input should error")
	}
}

func TestDecodeS32(t *testing.T) {
	v, noData, err := DecodeS32([]byte{0xFF, 0xFF, 0xFF, 0xFF}) // -1 W
	if err != nil || noData || v != -1 {
		t.Fatalf("got v=%d noData=%v err=%v", v, noData, err)
	}
	if v, nd, _ := DecodeS32([]byte{0x00, 0x00, 0x02, 0x00}); nd || v != 512 {
		t.Fatalf("512W: got v=%d noData=%v", v, nd)
	}
	if _, nd, _ := DecodeS32([]byte{0x80, 0x00, 0x00, 0x00}); !nd {
		t.Fatal("0x80000000 should be noData")
	}
	if _, nd, _ := DecodeS32([]byte{0x7F, 0xFF, 0xFF, 0xFE}); !nd {
		t.Fatal("0x7FFFFFFE should be noData")
	}
	if _, _, err := DecodeS32([]byte{0x01, 0x02, 0x03}); err == nil {
		t.Fatal("short input should error")
	}
}

func TestDecodeString(t *testing.T) {
	cases := map[string]string{
		string([]byte{'A', 'B', 'C', 0x00, 0x00}): "ABC", // 末尾の 0x00 を除去
		string([]byte{'X', 'Y', 'Z'}):             "XYZ", // 終端なし
		string([]byte{0x00, 0x00}):                "",    // 全て 0x00
		"":                                        "",    // 空
	}
	for in, want := range cases {
		if got := DecodeString([]byte(in)); got != want {
			t.Errorf("DecodeString(%x)=%q want %q", in, got, want)
		}
	}
}
