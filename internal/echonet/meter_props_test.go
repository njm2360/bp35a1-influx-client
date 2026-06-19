package echonet

import (
	"testing"
	"time"
)

func TestDecodeInstantPower(t *testing.T) {
	p, err := DecodeInstantPower([]byte{0x00, 0x00, 0x02, 0x00}) // 512W
	if err != nil || p.NoData || p.Watt != 512 {
		t.Fatalf("512W: got watt=%d noData=%v err=%v", p.Watt, p.NoData, err)
	}
	p, err = DecodeInstantPower([]byte{0xFF, 0xFF, 0xFF, 0xFF}) // -1W
	if err != nil || p.NoData || p.Watt != -1 {
		t.Fatalf("-1W: got watt=%d noData=%v err=%v", p.Watt, p.NoData, err)
	}
	for _, nd := range [][]byte{
		{0x80, 0x00, 0x00, 0x00}, // アンダーフロー
		{0x7F, 0xFF, 0xFF, 0xFF}, // オーバーフロー
		{0x7F, 0xFF, 0xFF, 0xFE}, // 計測データなし
	} {
		if p, err := DecodeInstantPower(nd); err != nil || !p.NoData {
			t.Fatalf("%x should be noData, got noData=%v err=%v", nd, p.NoData, err)
		}
	}
	if _, err := DecodeInstantPower([]byte{0x00}); err == nil {
		t.Fatal("short input should error")
	}
}

func TestDecodeCumulativeEnergy(t *testing.T) {
	e, err := DecodeCumulativeEnergy([]byte{0x00, 0x01, 0x86, 0xA0}) // 100000
	if err != nil || e.NoData || e.Raw != 100000 {
		t.Fatalf("got raw=%d noData=%v err=%v", e.Raw, e.NoData, err)
	}
	if e, err := DecodeCumulativeEnergy([]byte{0xFF, 0xFF, 0xFF, 0xFE}); err != nil || !e.NoData {
		t.Fatalf("0xFFFFFFFE should be noData, got %+v err=%v", e, err)
	}
}

func TestDecodeCoefficientAndDigits(t *testing.T) {
	if v, err := DecodeCoefficient([]byte{0x00, 0x00, 0x00, 0x0A}); err != nil || v != 10 {
		t.Fatalf("coefficient want 10, got %d err=%v", v, err)
	}
	if _, err := DecodeCoefficient([]byte{0x00}); err == nil {
		t.Fatal("short coefficient should error")
	}
	if d, err := DecodeDigits([]byte{0x08}); err != nil || d != 8 {
		t.Fatalf("digits want 8, got %d err=%v", d, err)
	}
	if _, err := DecodeDigits([]byte{0x01, 0x02}); err == nil {
		t.Fatal("wrong-length digits should error")
	}
}

func TestDecodeBRouteID(t *testing.T) {
	edt := make([]byte, 16)
	edt[1], edt[2], edt[3] = 0xAB, 0xCD, 0xEF // メーカコード
	id, err := DecodeBRouteID(edt)
	if err != nil {
		t.Fatal(err)
	}
	mc := id.MakerCode()
	if len(mc) != 3 || mc[0] != 0xAB || mc[1] != 0xCD || mc[2] != 0xEF {
		t.Fatalf("maker code want ABCDEF, got %x", mc)
	}
	if _, err := DecodeBRouteID(make([]byte, 8)); err == nil {
		t.Fatal("wrong-length b-route id should error")
	}
}

func TestDecodeHistoryCollectDay(t *testing.T) {
	if d, unset, err := DecodeHistoryCollectDay([]byte{0x05}); err != nil || unset || d != 5 {
		t.Fatalf("day want 5, got %d unset=%v err=%v", d, unset, err)
	}
	if _, unset, err := DecodeHistoryCollectDay([]byte{0xFF}); err != nil || !unset {
		t.Fatalf("0xFF should be unset, got unset=%v err=%v", unset, err)
	}
	if _, _, err := DecodeHistoryCollectDay(nil); err == nil {
		t.Fatal("empty should error")
	}
}

func TestEncodeHistoryCollectDay1(t *testing.T) {
	p, err := EncodeHistoryCollectDay1(5)
	if err != nil {
		t.Fatal(err)
	}
	if p.EPC != EPCHistoryDay1 {
		t.Fatalf("epc want 0xE5, got 0x%02X", p.EPC)
	}
	if len(p.EDT) != 1 || p.EDT[0] != 5 {
		t.Fatalf("edt want [05], got %x", p.EDT)
	}
	// 往復: エンコード→デコードで一致。
	day, unset, err := DecodeHistoryCollectDay(p.EDT)
	if err != nil || unset || day != 5 {
		t.Fatalf("round-trip failed: day=%d unset=%v err=%v", day, unset, err)
	}
	if _, err := EncodeHistoryCollectDay1(100); err == nil {
		t.Fatal("day 100 should error (range 0-99)")
	}
	if _, err := EncodeHistoryCollectDay1(-1); err == nil {
		t.Fatal("negative day should error")
	}
}

func TestEncodeHistoryCollectSpec2(t *testing.T) {
	jst := time.FixedZone("JST", 9*3600)
	tm := time.Date(2026, 6, 15, 21, 30, 0, 0, jst)
	p, err := EncodeHistoryCollectSpec2(tm, 12)
	if err != nil {
		t.Fatal(err)
	}
	if p.EPC != EPCHistoryDay2 {
		t.Fatalf("epc want 0xED, got 0x%02X", p.EPC)
	}
	// 往復: 7バイトをデコードして日時・コマ数が一致すること。
	s, err := DecodeHistoryCollectSpec(p.EDT, jst)
	if err != nil || s.Frames != 12 || !s.Time.Equal(tm) {
		t.Fatalf("round-trip failed: %+v err=%v", s, err)
	}
	// 分が0/30以外はエラー。
	if _, err := EncodeHistoryCollectSpec2(time.Date(2026, 6, 15, 21, 15, 0, 0, jst), 1); err == nil {
		t.Fatal("minute 15 should error (must be 0 or 30)")
	}
	// コマ数1～12の範囲外はエラー。
	if _, err := EncodeHistoryCollectSpec2(tm, 13); err == nil {
		t.Fatal("frames 13 should error (range 1-12)")
	}
}

func TestEncodeHistoryCollectSpec3(t *testing.T) {
	jst := time.FixedZone("JST", 9*3600)
	tm := time.Date(2026, 6, 15, 21, 59, 0, 0, jst)
	p, err := EncodeHistoryCollectSpec3(tm, 10)
	if err != nil {
		t.Fatal(err)
	}
	if p.EPC != EPCHistoryDay3 {
		t.Fatalf("epc want 0xEF, got 0x%02X", p.EPC)
	}
	s, err := DecodeHistoryCollectSpec(p.EDT, jst)
	if err != nil || s.Frames != 10 || !s.Time.Equal(tm) {
		t.Fatalf("round-trip failed: %+v err=%v", s, err)
	}
	// コマ数1～10の範囲外はエラー。
	if _, err := EncodeHistoryCollectSpec3(tm, 11); err == nil {
		t.Fatal("frames 11 should error (range 1-10)")
	}
}

// TestMeterSetPropsCoverage は AIF 表2-4 で Set 可能なEPCが過不足なく登録され、
// それぞれのエンコーダが正しい EPC を生成することを保証する。
func TestMeterSetPropsCoverage(t *testing.T) {
	want := []byte{EPCHistoryDay1, EPCHistoryDay2, EPCHistoryDay3}
	got := SettableMeterEPCs()
	if len(got) != len(want) {
		t.Fatalf("settable EPCs want %x, got %x", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("settable EPCs want %x, got %x", want, got)
		}
		if _, ok := MeterEPCSettable(want[i]); !ok {
			t.Fatalf("0x%02X should be settable", want[i])
		}
	}
}

// TestMeterPropsCoverage は AIF 表2-4 に規定された全 EPC が
// ルート(meterProps)に登録されていることを保証する。
func TestMeterPropsCoverage(t *testing.T) {
	required := []byte{
		EPCOperationStatus, EPCBRouteID, EPCCumulative1Min, EPCCoefficient,
		EPCDigits, EPCCumulativeFwd, EPCUnit, EPCCumulativeHistory1Fwd,
		EPCCumulativeRev, EPCCumulativeHistory1Rev, EPCHistoryDay1,
		EPCInstantPower, EPCInstantCurrent, EPCScheduledFwd, EPCScheduledRev,
		EPCCumulativeHistory2, EPCHistoryDay2, EPCCumulativeHistory3, EPCHistoryDay3,
	}
	for _, epc := range required {
		if !MeterEPCSupported(epc) {
			t.Errorf("EPC 0x%02X is in AIF 表2-4 but has no decoder in meterProps", epc)
		}
		if name, ok := MeterEPCName(epc); !ok || name == "" {
			t.Errorf("EPC 0x%02X has no name", epc)
		}
	}
}

// TestDecodeMeterProp は代表的な EPC についてルート経由のデコードが
// 型付き値を返すことを確認する。
func TestDecodeMeterProp(t *testing.T) {
	jst := time.FixedZone("JST", 9*3600)

	v, name, ok, err := DecodeMeterProp(EPCInstantPower, []byte{0x00, 0x00, 0x02, 0x00}, jst)
	if !ok || err != nil {
		t.Fatalf("0xE7 decode failed: ok=%v err=%v", ok, err)
	}
	if p, isType := v.(InstantPower); !isType || p.Watt != 512 {
		t.Fatalf("0xE7 want InstantPower{512}, got %T %v (name=%q)", v, v, name)
	}

	v, _, ok, err = DecodeMeterProp(EPCScheduledFwd,
		[]byte{0x07, 0xEA, 0x06, 0x0F, 0x0A, 0x1E, 0x00, 0x00, 0x01, 0xE2, 0x40}, jst)
	if !ok || err != nil {
		t.Fatalf("0xEA decode failed: ok=%v err=%v", ok, err)
	}
	if s, isType := v.(Scheduled); !isType || s.Raw != 123456 {
		t.Fatalf("0xEA want Scheduled{raw=123456}, got %T %v", v, v)
	}

	// 未対応 EPC は ok=false。
	if _, _, ok, _ := DecodeMeterProp(0x00, nil, jst); ok {
		t.Fatal("unknown EPC should report ok=false")
	}
}

// TestDecodeMeterPropExtra は TestDecodeMeterProp が触れていないルート
// (decodeUnit/decodeHistoryDay/decodeCurrent)を経由させる。
func TestDecodeMeterPropExtra(t *testing.T) {
	jst := time.FixedZone("JST", 9*3600)

	// 0xE1 積算電力量単位 → decodeUnit → float64
	if v, _, ok, err := DecodeMeterProp(EPCUnit, []byte{0x01}, jst); !ok || err != nil {
		t.Fatalf("0xE1 decode failed: ok=%v err=%v", ok, err)
	} else if u, isType := v.(float64); !isType || u != 0.1 {
		t.Fatalf("0xE1 want 0.1, got %T %v", v, v)
	}
	if _, _, _, err := DecodeMeterProp(EPCUnit, []byte{0x01, 0x02}, jst); err == nil {
		t.Fatal("0xE1 wrong length should error")
	}

	// 0xE5 積算履歴収集日1 → decodeHistoryDay
	if v, _, ok, err := DecodeMeterProp(EPCHistoryDay1, []byte{0xFF}, jst); !ok || err != nil {
		t.Fatalf("0xE5 decode failed: ok=%v err=%v", ok, err)
	} else if d, isType := v.(struct {
		Day   int
		Unset bool
	}); !isType || !d.Unset {
		t.Fatalf("0xE5 0xFF want Unset=true, got %T %v", v, v)
	}

	// 0xE8 瞬時電流計測値 → decodeCurrent → InstantCurrent
	if v, _, ok, err := DecodeMeterProp(EPCInstantCurrent, []byte{0x00, 0x19, 0x7F, 0xFE}, jst); !ok || err != nil {
		t.Fatalf("0xE8 decode failed: ok=%v err=%v", ok, err)
	} else if c, isType := v.(InstantCurrent); !isType || c.R != 2.5 || !c.TNoData {
		t.Fatalf("0xE8 want R=2.5 TNoData, got %T %v", v, v)
	}

	// デコードエラーは err として伝播すること(ok=true, err!=nil)。
	if _, _, ok, err := DecodeMeterProp(EPCInstantCurrent, []byte{0x00}, jst); !ok || err == nil {
		t.Fatalf("0xE8 short edt: want ok=true err!=nil, got ok=%v err=%v", ok, err)
	}
}

func TestMeterEPCsSortedAndNameLookup(t *testing.T) {
	epcs := MeterEPCs()
	if len(epcs) == 0 {
		t.Fatal("MeterEPCs returned empty")
	}
	for i := 1; i < len(epcs); i++ {
		if epcs[i-1] >= epcs[i] {
			t.Fatalf("MeterEPCs not strictly sorted: %x", epcs)
		}
	}
	if name, ok := MeterEPCName(EPCInstantPower); !ok || name == "" {
		t.Fatalf("0xE7 name lookup failed: ok=%v name=%q", ok, name)
	}
	if _, ok := MeterEPCName(0x00); ok {
		t.Fatal("unknown EPC name lookup should report ok=false")
	}
}
