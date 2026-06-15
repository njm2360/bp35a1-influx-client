package echonet

// 低圧スマート電力量メータクラス(0x02/0x88)固有の EPC とデコーダ。

import (
	"encoding/binary"
	"fmt"
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

// 0xD0 1分積算電力量計測値
func DecodeCumulative1Min(edt []byte, loc *time.Location) (t time.Time, fwd uint32, noData bool, err error) {
	if len(edt) != 15 {
		return time.Time{}, 0, false, fmt.Errorf("echonet: cumulative1min expects 15 bytes, got %d", len(edt))
	}
	t = time.Date(
		int(binary.BigEndian.Uint16(edt[0:2])),
		time.Month(edt[2]), int(edt[3]),
		int(edt[4]), int(edt[5]), int(edt[6]), 0, loc,
	)
	fwd = binary.BigEndian.Uint32(edt[7:11])
	if fwd == noData32 {
		noData = true
	}
	return t, fwd, noData, nil
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
