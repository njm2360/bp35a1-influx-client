package echonet

import (
	"encoding/binary"
	"fmt"
	"slices"
	"time"
)

const (
	EPCBRouteID              byte = 0xC0 // Bルート識別番号
	EPCCumulative1Min        byte = 0xD0 // 1分積算電力量計測値
	EPCCoefficient           byte = 0xD3 // 係数
	EPCDigits                byte = 0xD7 // 積算電力量有効桁数
	EPCCumulativeFwd         byte = 0xE0 // 積算電力量計測値(正方向)
	EPCUnit                  byte = 0xE1 // 積算電力量単位
	EPCCumulativeHistory1Fwd byte = 0xE2 // 積算電力量計測値履歴1(正方向)
	EPCCumulativeRev         byte = 0xE3 // 積算電力量計測値(逆方向)
	EPCCumulativeHistory1Rev byte = 0xE4 // 積算電力量計測値履歴1(逆方向)
	EPCHistoryDay1           byte = 0xE5 // 積算履歴収集日1
	EPCInstantPower          byte = 0xE7 // 瞬時電力計測値
	EPCInstantCurrent        byte = 0xE8 // 瞬時電流計測値
	EPCScheduledFwd          byte = 0xEA // 定時積算電力量計測値(正方向)
	EPCScheduledRev          byte = 0xEB // 定時積算電力量計測値(逆方向)
	EPCCumulativeHistory2    byte = 0xEC // 積算電力量計測値履歴2
	EPCHistoryDay2           byte = 0xED // 積算履歴収集日2
	EPCCumulativeHistory3    byte = 0xEE // 積算電力量計測値履歴3
	EPCHistoryDay3           byte = 0xEF // 積算履歴収集日3
)

// 0xD0 1分積算電力量計測値(正方向・逆方向計測値)
type Cumulative1Min struct {
	Time                 time.Time
	Fwd, Rev             uint32
	FwdNoData, RevNoData bool
}

// 0xD0 1分積算電力量計測値
func DecodeCumulative1Min(edt []byte, loc *time.Location) (Cumulative1Min, error) {
	if len(edt) != 15 {
		return Cumulative1Min{}, fmt.Errorf("echonet: cumulative1min expects 15 bytes, got %d", len(edt))
	}
	fwd := binary.BigEndian.Uint32(edt[7:11])
	rev := binary.BigEndian.Uint32(edt[11:15])
	return Cumulative1Min{
		Time: time.Date(
			int(binary.BigEndian.Uint16(edt[0:2])),
			time.Month(edt[2]), int(edt[3]),
			int(edt[4]), int(edt[5]), int(edt[6]), 0, loc,
		),
		Fwd:       fwd,
		Rev:       rev,
		FwdNoData: fwd == noData32,
		RevNoData: rev == noData32,
	}, nil
}

// 0xC0 Bルート識別番号(16バイト)。
type BRouteID struct {
	Raw []byte
}

func (b BRouteID) MakerCode() []byte {
	if len(b.Raw) < 4 {
		return nil
	}
	return b.Raw[1:4]
}

// 0xC0 Bルート識別番号
func DecodeBRouteID(edt []byte) (BRouteID, error) {
	if len(edt) != 16 {
		return BRouteID{}, fmt.Errorf("echonet: b-route id expects 16 bytes, got %d", len(edt))
	}
	return BRouteID{Raw: slices.Clone(edt)}, nil
}

// 0xD3 係数
func DecodeCoefficient(edt []byte) (uint32, error) {
	if len(edt) != 4 {
		return 0, fmt.Errorf("echonet: coefficient expects 4 bytes, got %d", len(edt))
	}
	return binary.BigEndian.Uint32(edt), nil
}

// 0xD7 積算電力量有効桁数(1～8)
func DecodeDigits(edt []byte) (int, error) {
	if len(edt) != 1 {
		return 0, fmt.Errorf("echonet: digits expects 1 byte, got %d", len(edt))
	}
	return int(edt[0]), nil
}

// 0xE0/0xE3 積算電力量計測値(正方向・逆方向)
type CumulativeEnergy struct {
	Raw    uint32
	NoData bool
}

// 0xE0/0xE3 積算電力量計測値
func DecodeCumulativeEnergy(edt []byte) (CumulativeEnergy, error) {
	v, noData, err := DecodeU32(edt)
	if err != nil {
		return CumulativeEnergy{}, err
	}
	return CumulativeEnergy{Raw: v, NoData: noData}, nil
}

// 0xE5 積算履歴収集日1
func DecodeHistoryCollectDay(edt []byte) (day int, unset bool, err error) {
	if len(edt) != 1 {
		return 0, false, fmt.Errorf("echonet: history collect day expects 1 byte, got %d", len(edt))
	}
	if edt[0] == 0xFF {
		return 0, true, nil
	}
	return int(edt[0]), false, nil
}

// 0xE5 積算履歴収集日1
func EncodeHistoryCollectDay1(day int) (Property, error) {
	if day < 0 || day > 99 {
		return Property{}, fmt.Errorf("echonet: history collect day out of range: %d (want 0-99)", day)
	}
	return Property{EPC: EPCHistoryDay1, EDT: []byte{byte(day)}}, nil
}

// 0xE7 瞬時電力計測値
type InstantPower struct {
	Watt   int32
	NoData bool
}

// 0xE7 瞬時電力計測値
func DecodeInstantPower(edt []byte) (InstantPower, error) {
	if len(edt) != 4 {
		return InstantPower{}, fmt.Errorf("echonet: instant power expects 4 bytes, got %d", len(edt))
	}
	u := binary.BigEndian.Uint32(edt)
	switch u {
	case 0x80000000, 0x7FFFFFFF, 0x7FFFFFFE:
		return InstantPower{Watt: int32(u), NoData: true}, nil
	}
	return InstantPower{Watt: int32(u)}, nil
}

// 0xE1 積算電力量単位
func UnitKwh(code byte) float64 {
	switch code {
	case 0x00:
		return 1
	case 0x01:
		return 0.1
	case 0x02:
		return 0.01
	case 0x03:
		return 0.001
	case 0x04:
		return 0.0001
	case 0x0A:
		return 10
	case 0x0B:
		return 100
	case 0x0C:
		return 1000
	case 0x0D:
		return 10000
	default:
		return 0
	}
}

// 0xE2/0xE4 積算電力量計測値履歴1
type CumulativeHistory1 struct {
	Day    int
	Values [48]uint32
	NoData [48]bool
}

// 0xE2/0xE4 積算電力量計測値履歴1
func DecodeCumulativeHistory1(edt []byte) (CumulativeHistory1, error) {
	const want = 2 + 4*48
	if len(edt) != want {
		return CumulativeHistory1{}, fmt.Errorf("echonet: history1 expects %d bytes, got %d", want, len(edt))
	}
	var h CumulativeHistory1
	h.Day = int(binary.BigEndian.Uint16(edt[0:2]))
	for i := range 48 {
		off := 2 + i*4
		v := binary.BigEndian.Uint32(edt[off : off+4])
		h.Values[i] = v
		h.NoData[i] = v == noData32
	}
	return h, nil
}

// 0xE8 瞬時電流計測値(R相・T相)
type InstantCurrent struct {
	R, T             float64
	RNoData, TNoData bool
}

// 0xE8 瞬時電流計測値
func DecodeCurrent(edt []byte) (rA float64, tA float64, rNoData, tNoData bool, err error) {
	if len(edt) != 4 {
		return 0, 0, false, false, fmt.Errorf("echonet: current expects 4 bytes, got %d", len(edt))
	}
	r := int16(binary.BigEndian.Uint16(edt[0:2]))
	t := int16(binary.BigEndian.Uint16(edt[2:4]))
	if uint16(r) == 0x7FFE {
		rNoData = true
	}
	if uint16(t) == 0x7FFE {
		tNoData = true
	}
	return float64(r) / 10.0, float64(t) / 10.0, rNoData, tNoData, nil
}

// 0xEA/0xEB 定時積算電力量計測値(正方向・逆方向)
type Scheduled struct {
	Time   time.Time
	Raw    uint32
	NoData bool
}

// 0xEA/0xEB 定時積算電力量計測値
func DecodeScheduled30(edt []byte, loc *time.Location) (t time.Time, raw uint32, noData bool, err error) {
	if len(edt) != 11 {
		return time.Time{}, 0, false, fmt.Errorf("echonet: scheduled30 expects 11 bytes, got %d", len(edt))
	}
	t = time.Date(
		int(binary.BigEndian.Uint16(edt[0:2])),
		time.Month(edt[2]), int(edt[3]),
		int(edt[4]), int(edt[5]), int(edt[6]), 0, loc,
	)
	raw = binary.BigEndian.Uint32(edt[7:11])
	if raw == noData32 {
		noData = true
	}
	return t, raw, noData, nil
}

// 0xEC/0xEE 積算電力量計測値履歴2・3
type CumulativeHistoryEntry struct {
	Fwd, Rev             uint32
	FwdNoData, RevNoData bool
}

// 0xEC/0xEE 積算電力量計測値履歴2・3
type CumulativeHistory struct {
	Time    time.Time
	Entries []CumulativeHistoryEntry
}

// 0xEC/0xEE 積算電力量計測値履歴2・3
func DecodeCumulativeHistory(edt []byte, loc *time.Location) (CumulativeHistory, error) {
	if len(edt) < 7 {
		return CumulativeHistory{}, fmt.Errorf("echonet: cumulative history too short: %d bytes", len(edt))
	}
	year := int(binary.BigEndian.Uint16(edt[0:2]))
	month := time.Month(edt[2])
	day := int(edt[3])
	hour := int(edt[4])
	min := int(edt[5])
	frames := int(edt[6])
	body := edt[7:]
	if len(body) != frames*8 {
		return CumulativeHistory{}, fmt.Errorf("echonet: cumulative history body=%d bytes, want frames*8 (frames=%d)", len(body), frames)
	}
	h := CumulativeHistory{
		Time:    time.Date(year, month, day, hour, min, 0, 0, loc),
		Entries: make([]CumulativeHistoryEntry, frames),
	}
	for i := range frames {
		off := i * 8
		fwd := binary.BigEndian.Uint32(body[off : off+4])
		rev := binary.BigEndian.Uint32(body[off+4 : off+8])
		h.Entries[i] = CumulativeHistoryEntry{
			Fwd:       fwd,
			Rev:       rev,
			FwdNoData: fwd == noData32,
			RevNoData: rev == noData32,
		}
	}
	return h, nil
}

// 0xED/0xEF 積算履歴収集日2・3
type HistoryCollectSpec struct {
	Time   time.Time
	Frames int
}

// 0xED/0xEF 積算履歴収集日2・3
func DecodeHistoryCollectSpec(edt []byte, loc *time.Location) (HistoryCollectSpec, error) {
	if len(edt) != 7 {
		return HistoryCollectSpec{}, fmt.Errorf("echonet: history collect spec expects 7 bytes, got %d", len(edt))
	}
	return HistoryCollectSpec{
		Time: time.Date(
			int(binary.BigEndian.Uint16(edt[0:2])),
			time.Month(edt[2]), int(edt[3]),
			int(edt[4]), int(edt[5]), 0, 0, loc,
		),
		Frames: int(edt[6]),
	}, nil
}

func encodeHistorySpec(t time.Time, frames int) ([]byte, error) {
	y := t.Year()
	if y < 1 || y > 9999 {
		return nil, fmt.Errorf("echonet: history spec year out of range: %d (want 1-9999)", y)
	}
	edt := make([]byte, 7)
	binary.BigEndian.PutUint16(edt[0:2], uint16(y))
	edt[2] = byte(t.Month())
	edt[3] = byte(t.Day())
	edt[4] = byte(t.Hour())
	edt[5] = byte(t.Minute())
	edt[6] = byte(frames)
	return edt, nil
}

// 0xED 積算履歴収集日2 エンコーダ
func EncodeHistoryCollectSpec2(t time.Time, frames int) (Property, error) {
	if m := t.Minute(); m != 0 && m != 30 {
		return Property{}, fmt.Errorf("echonet: history2 minute must be 0 or 30, got %d", m)
	}
	if frames < 1 || frames > 12 {
		return Property{}, fmt.Errorf("echonet: history2 frames must be 1-12, got %d", frames)
	}
	edt, err := encodeHistorySpec(t, frames)
	if err != nil {
		return Property{}, err
	}
	return Property{EPC: EPCHistoryDay2, EDT: edt}, nil
}

// 0xEF 積算履歴収集日3 エンコーダ
func EncodeHistoryCollectSpec3(t time.Time, frames int) (Property, error) {
	if frames < 1 || frames > 10 {
		return Property{}, fmt.Errorf("echonet: history3 frames must be 1-10, got %d", frames)
	}
	edt, err := encodeHistorySpec(t, frames)
	if err != nil {
		return Property{}, err
	}
	return Property{EPC: EPCHistoryDay3, EDT: edt}, nil
}
