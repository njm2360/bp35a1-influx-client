package echonet

import (
	"testing"
	"time"
)

func TestDecodeCurrent(t *testing.T) {
	// R=2.5A (0x0019=25), T 未計測(0x7FFE)
	r, _, rNo, tNo, err := DecodeCurrent([]byte{0x00, 0x19, 0x7F, 0xFE})
	if err != nil {
		t.Fatal(err)
	}
	if r != 2.5 || rNo {
		t.Fatalf("R want 2.5 valid, got %v noData=%v", r, rNo)
	}
	if !tNo {
		t.Fatal("T should be noData (0x7FFE)")
	}
}

func TestDecodeCurrentBothValidAndNegative(t *testing.T) {
	// R=1.0A(0x000A=10), T=-1.0A(0xFFF6=-10、符号付き)
	r, tt, rNo, tNo, err := DecodeCurrent([]byte{0x00, 0x0A, 0xFF, 0xF6})
	if err != nil {
		t.Fatal(err)
	}
	if rNo || tNo {
		t.Fatalf("both should be valid, rNo=%v tNo=%v", rNo, tNo)
	}
	if r != 1.0 || tt != -1.0 {
		t.Fatalf("want R=1.0 T=-1.0, got R=%v T=%v", r, tt)
	}
}

func TestDecodeCurrentBothNoData(t *testing.T) {
	_, _, rNo, tNo, err := DecodeCurrent([]byte{0x7F, 0xFE, 0x7F, 0xFE})
	if err != nil {
		t.Fatal(err)
	}
	if !rNo || !tNo {
		t.Fatalf("both should be noData, rNo=%v tNo=%v", rNo, tNo)
	}
}

func TestDecodeCurrentWrongLength(t *testing.T) {
	if _, _, _, _, err := DecodeCurrent([]byte{0x00, 0x19}); err == nil {
		t.Fatal("wrong length should error")
	}
}

func TestUnitKwh(t *testing.T) {
	cases := map[byte]float64{
		0x00: 1, 0x01: 0.1, 0x02: 0.01, 0x03: 0.001, 0x04: 0.0001,
		0x0A: 10, 0x0B: 100, 0x0C: 1000, 0x0D: 10000,
		0x05: 0, 0xEE: 0, // 未定義コードは換算不能(0)
	}
	for code, want := range cases {
		if got := UnitKwh(code); got != want {
			t.Errorf("UnitKwh(%#x)=%v want %v", code, got, want)
		}
	}
}

func TestDecodeCumulative1Min(t *testing.T) {
	jst := time.FixedZone("JST", 9*3600)
	// 2026-06-15 10:30:00 / 正方向 123456(0x0001E240) / 逆方向 999(0x000003E7)
	edt := []byte{
		0x07, 0xEA, 0x06, 0x0F, // 年月日
		0x0A, 0x1E, 0x00, // 時分秒
		0x00, 0x01, 0xE2, 0x40, // 正方向積算
		0x00, 0x00, 0x03, 0xE7, // 逆方向積算
	}
	c, err := DecodeCumulative1Min(edt, jst)
	if err != nil || c.FwdNoData || c.RevNoData {
		t.Fatalf("err=%v fwdNoData=%v revNoData=%v", err, c.FwdNoData, c.RevNoData)
	}
	if c.Fwd != 123456 {
		t.Fatalf("fwd want 123456, got %d", c.Fwd)
	}
	if c.Rev != 999 {
		t.Fatalf("rev want 999, got %d", c.Rev)
	}
	want := time.Date(2026, 6, 15, 10, 30, 0, 0, jst)
	if !c.Time.Equal(want) {
		t.Fatalf("time want %v, got %v", want, c.Time)
	}
}

func TestDecodeCumulative1MinNoData(t *testing.T) {
	jst := time.FixedZone("JST", 9*3600)
	edt := []byte{
		0x07, 0xEA, 0x06, 0x0F, 0x0A, 0x1E, 0x00,
		0xFF, 0xFF, 0xFF, 0xFE, // 正方向 noData
		0xFF, 0xFF, 0xFF, 0xFE, // 逆方向 noData
	}
	c, err := DecodeCumulative1Min(edt, jst)
	if err != nil || !c.FwdNoData || !c.RevNoData {
		t.Fatalf("0xFFFFFFFE should be noData, got fwd=%v rev=%v err=%v", c.FwdNoData, c.RevNoData, err)
	}
}

func TestDecodeCumulative1MinWrongLength(t *testing.T) {
	// 旧実装が誤って想定していた 8byte は弾く
	if _, err := DecodeCumulative1Min(make([]byte, 8), time.UTC); err == nil {
		t.Fatal("8 bytes should error (spec is 15 bytes)")
	}
}

func TestDecodeScheduled30(t *testing.T) {
	jst := time.FixedZone("JST", 9*3600)
	// 2026-06-15 10:30:00 + 積算 123456
	edt := []byte{0x07, 0xEA, 0x06, 0x0F, 0x0A, 0x1E, 0x00, 0x00, 0x01, 0xE2, 0x40}
	tm, raw, noData, err := DecodeScheduled30(edt, jst)
	if err != nil || noData {
		t.Fatalf("err=%v noData=%v", err, noData)
	}
	if raw != 123456 {
		t.Fatalf("raw want 123456, got %d", raw)
	}
	want := time.Date(2026, 6, 15, 10, 30, 0, 0, jst)
	if !tm.Equal(want) {
		t.Fatalf("time want %v, got %v", want, tm)
	}
}

func TestDecodeScheduled30NoData(t *testing.T) {
	jst := time.FixedZone("JST", 9*3600)
	edt := []byte{0x07, 0xEA, 0x06, 0x0F, 0x0A, 0x1E, 0x00, 0xFF, 0xFF, 0xFF, 0xFE} // raw=noData
	_, _, noData, err := DecodeScheduled30(edt, jst)
	if err != nil {
		t.Fatal(err)
	}
	if !noData {
		t.Fatal("0xFFFFFFFE raw should be noData")
	}
}

func TestDecodeScheduled30WrongLength(t *testing.T) {
	if _, _, _, err := DecodeScheduled30([]byte{0x07, 0xEA}, time.UTC); err == nil {
		t.Fatal("wrong length should error")
	}
}

func TestDecodeCumulativeHistory1(t *testing.T) {
	edt := make([]byte, 2+4*48)
	edt[0], edt[1] = 0x00, 0x05 // 収集日 = 5
	// コマ0 = 100000(0x000186A0), コマ47 = noData(0xFFFFFFFE)
	copy(edt[2:6], []byte{0x00, 0x01, 0x86, 0xA0})
	copy(edt[2+47*4:2+48*4], []byte{0xFF, 0xFF, 0xFF, 0xFE})
	h, err := DecodeCumulativeHistory1(edt)
	if err != nil {
		t.Fatal(err)
	}
	if h.Day != 5 {
		t.Fatalf("day want 5, got %d", h.Day)
	}
	if h.Values[0] != 100000 || h.NoData[0] {
		t.Fatalf("frame0 want 100000 valid, got %d noData=%v", h.Values[0], h.NoData[0])
	}
	if !h.NoData[47] {
		t.Fatal("frame47 should be noData")
	}
	if _, err := DecodeCumulativeHistory1(make([]byte, 100)); err == nil {
		t.Fatal("wrong length should error")
	}
}

func TestDecodeCumulativeHistory(t *testing.T) {
	jst := time.FixedZone("JST", 9*3600)
	// 2026-06-15 21:30, 2コマ: (正100000,逆10), (正200000,逆noData)
	edt := []byte{
		0x07, 0xEA, 0x06, 0x0F, 0x15, 0x1E, // 日時 21:30
		0x02, // コマ数
		0x00, 0x01, 0x86, 0xA0, 0x00, 0x00, 0x00, 0x0A,
		0x00, 0x03, 0x0D, 0x40, 0xFF, 0xFF, 0xFF, 0xFE,
	}
	h, err := DecodeCumulativeHistory(edt, jst)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(h.Entries))
	}
	if h.Entries[0].Fwd != 100000 || h.Entries[0].Rev != 10 {
		t.Fatalf("entry0 unexpected: %+v", h.Entries[0])
	}
	if h.Entries[1].Fwd != 200000 || !h.Entries[1].RevNoData {
		t.Fatalf("entry1 unexpected: %+v", h.Entries[1])
	}
	want := time.Date(2026, 6, 15, 21, 30, 0, 0, jst)
	if !h.Time.Equal(want) {
		t.Fatalf("time want %v, got %v", want, h.Time)
	}
	// コマ数とボディ長の不整合はエラー。
	if _, err := DecodeCumulativeHistory([]byte{0x07, 0xEA, 0x06, 0x0F, 0x15, 0x1E, 0x02, 0x00}, jst); err == nil {
		t.Fatal("body length mismatch should error")
	}
}

func TestDecodeHistoryCollectSpec(t *testing.T) {
	jst := time.FixedZone("JST", 9*3600)
	edt := []byte{0x07, 0xEA, 0x06, 0x0F, 0x15, 0x00, 0x0C} // 2026-06-15 21:00, 12コマ
	s, err := DecodeHistoryCollectSpec(edt, jst)
	if err != nil {
		t.Fatal(err)
	}
	if s.Frames != 12 {
		t.Fatalf("frames want 12, got %d", s.Frames)
	}
	want := time.Date(2026, 6, 15, 21, 0, 0, 0, jst)
	if !s.Time.Equal(want) {
		t.Fatalf("time want %v, got %v", want, s.Time)
	}
	if _, err := DecodeHistoryCollectSpec(make([]byte, 6), jst); err == nil {
		t.Fatal("wrong length should error")
	}
}
