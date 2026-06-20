package echonet

import (
	"fmt"
	"maps"
	"slices"
	"time"
)

type PropSpec struct {
	Name   string
	Decode func(edt []byte, loc *time.Location) (any, error)
}

var meterProps = map[byte]PropSpec{
	// --- 機器オブジェクトスーパークラス ---
	EPCOperationStatus: {"動作状態", func(e []byte, _ *time.Location) (any, error) { return DecodeOperationStatus(e) }},
	EPCStandardVersion: {"規格Version情報", func(e []byte, _ *time.Location) (any, error) { return slices.Clone(e), nil }},
	EPCFaultStatus:     {"異常発生状態", func(e []byte, _ *time.Location) (any, error) { return DecodeFaultStatus(e) }},
	EPCMakerCode:       {"メーカコード", func(e []byte, _ *time.Location) (any, error) { return slices.Clone(e), nil }},
	EPCSerialNumber:    {"製造番号", func(e []byte, _ *time.Location) (any, error) { return DecodeString(e), nil }},
	EPCStatusChangeMap: {"状変アナウンスプロパティマップ", func(e []byte, _ *time.Location) (any, error) { return DecodePropertyMap(e) }},
	EPCSetPropertyMap:  {"Setプロパティマップ", func(e []byte, _ *time.Location) (any, error) { return DecodePropertyMap(e) }},
	EPCGetPropertyMap:  {"Getプロパティマップ", func(e []byte, _ *time.Location) (any, error) { return DecodePropertyMap(e) }},

	// --- 低圧スマート電力量メータクラス ---
	EPCBRouteID:              {"Bルート識別番号", func(e []byte, _ *time.Location) (any, error) { return DecodeBRouteID(e) }},
	EPCCumulative1Min:        {"1分積算電力量計測値", func(e []byte, loc *time.Location) (any, error) { return DecodeCumulative1Min(e, loc) }},
	EPCCoefficient:           {"係数", func(e []byte, _ *time.Location) (any, error) { return DecodeCoefficient(e) }},
	EPCDigits:                {"積算電力量有効桁数", func(e []byte, _ *time.Location) (any, error) { return DecodeDigits(e) }},
	EPCCumulativeFwd:         {"積算電力量計測値(正方向)", func(e []byte, _ *time.Location) (any, error) { return DecodeCumulativeEnergy(e) }},
	EPCUnit:                  {"積算電力量単位", func(e []byte, _ *time.Location) (any, error) { return decodeUnit(e) }},
	EPCCumulativeHistory1Fwd: {"積算電力量計測値履歴1(正方向)", func(e []byte, _ *time.Location) (any, error) { return DecodeCumulativeHistory1(e) }},
	EPCCumulativeRev:         {"積算電力量計測値(逆方向)", func(e []byte, _ *time.Location) (any, error) { return DecodeCumulativeEnergy(e) }},
	EPCCumulativeHistory1Rev: {"積算電力量計測値履歴1(逆方向)", func(e []byte, _ *time.Location) (any, error) { return DecodeCumulativeHistory1(e) }},
	EPCHistoryDay1:           {"積算履歴収集日1", func(e []byte, _ *time.Location) (any, error) { return decodeHistoryDay(e) }},
	EPCInstantPower:          {"瞬時電力計測値", func(e []byte, _ *time.Location) (any, error) { return DecodeInstantPower(e) }},
	EPCInstantCurrent:        {"瞬時電流計測値", func(e []byte, _ *time.Location) (any, error) { return decodeCurrent(e) }},
	EPCScheduledFwd:          {"定時積算電力量計測値(正方向)", func(e []byte, loc *time.Location) (any, error) { return decodeScheduled(e, loc) }},
	EPCScheduledRev:          {"定時積算電力量計測値(逆方向)", func(e []byte, loc *time.Location) (any, error) { return decodeScheduled(e, loc) }},
	EPCCumulativeHistory2:    {"積算電力量計測値履歴2", func(e []byte, loc *time.Location) (any, error) { return DecodeCumulativeHistory(e, loc) }},
	EPCHistoryDay2:           {"積算履歴収集日2", func(e []byte, loc *time.Location) (any, error) { return DecodeHistoryCollectSpec(e, loc) }},
	EPCCumulativeHistory3:    {"積算電力量計測値履歴3", func(e []byte, loc *time.Location) (any, error) { return DecodeCumulativeHistory(e, loc) }},
	EPCHistoryDay3:           {"積算履歴収集日3", func(e []byte, loc *time.Location) (any, error) { return DecodeHistoryCollectSpec(e, loc) }},
}

func decodeUnit(edt []byte) (any, error) {
	if len(edt) != 1 {
		return nil, fmt.Errorf("echonet: unit expects 1 byte, got %d", len(edt))
	}
	return UnitKwh(edt[0]), nil
}

func decodeHistoryDay(edt []byte) (any, error) {
	day, unset, err := DecodeHistoryCollectDay(edt)
	if err != nil {
		return nil, err
	}
	return struct {
		Day   int
		Unset bool
	}{day, unset}, nil
}

func decodeCurrent(edt []byte) (any, error) {
	r, t, rNo, tNo, err := DecodeCurrent(edt)
	if err != nil {
		return nil, err
	}
	return InstantCurrent{R: r, T: t, RNoData: rNo, TNoData: tNo}, nil
}

func decodeScheduled(edt []byte, loc *time.Location) (any, error) {
	t, raw, noData, err := DecodeScheduled30(edt, loc)
	if err != nil {
		return nil, err
	}
	return Scheduled{Time: t, Raw: raw, NoData: noData}, nil
}

var meterSetProps = map[byte]string{
	EPCHistoryDay1: "積算履歴収集日1",
	EPCHistoryDay2: "積算履歴収集日2",
	EPCHistoryDay3: "積算履歴収集日3",
}

func MeterEPCSettable(epc byte) (string, bool) {
	name, ok := meterSetProps[epc]
	return name, ok
}

func SettableMeterEPCs() []byte {
	return slices.Sorted(maps.Keys(meterSetProps))
}

func DecodeMeterProp(epc byte, edt []byte, loc *time.Location) (value any, name string, ok bool, err error) {
	spec, found := meterProps[epc]
	if !found {
		return nil, "", false, nil
	}
	v, e := spec.Decode(edt, loc)
	return v, spec.Name, true, e
}

func MeterEPCName(epc byte) (string, bool) {
	spec, ok := meterProps[epc]
	if !ok {
		return "", false
	}
	return spec.Name, true
}

func MeterEPCSupported(epc byte) bool {
	_, ok := meterProps[epc]
	return ok
}

func MeterEPCs() []byte {
	return slices.Sorted(maps.Keys(meterProps))
}
