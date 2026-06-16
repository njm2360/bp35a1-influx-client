package collector

import (
	"encoding/hex"
	"fmt"
	"time"

	"main/internal/echonet"
	"main/internal/model"
)

func toPower(f echonet.Frame, now time.Time) (model.Power, error) {
	out := model.Power{Time: now}

	edt, ok := f.EDT(echonet.EPCInstantPower)
	if !ok {
		return model.Power{}, fmt.Errorf("toPower: missing EPC 0xE7")
	}
	p, err := echonet.DecodeInstantPower(edt)
	if err != nil {
		return model.Power{}, err
	}
	out.Watt, out.HasWatt = p.Watt, !p.NoData

	if edt, ok := f.EDT(echonet.EPCInstantCurrent); ok {
		r, t, rNo, tNo, err := echonet.DecodeCurrent(edt)
		if err != nil {
			return model.Power{}, err
		}
		out.CurrentR, out.HasCurrentR = r, !rNo
		out.CurrentT, out.HasCurrentT = t, !tNo
	}
	return out, nil
}

func toEnergy(edt []byte, p model.MeterParams) (kwh float64, raw uint32, ok bool, err error) {
	e, err := echonet.DecodeCumulativeEnergy(edt)
	if err != nil {
		return 0, 0, false, err
	}
	if e.NoData {
		return 0, 0, false, nil
	}
	if !p.Valid() {
		return 0, e.Raw, false, fmt.Errorf("toEnergy: meter params not ready")
	}
	return p.ToKWh(e.Raw), e.Raw, true, nil
}

func toEnergy1Min(f echonet.Frame, params model.MeterParams, loc *time.Location) (model.Energy1Min, bool, error) {
	edt, ok := f.EDT(echonet.EPCCumulative1Min)
	if !ok || len(edt) == 0 {
		return model.Energy1Min{}, false, nil
	}
	c, err := echonet.DecodeCumulative1Min(edt, loc)
	if err != nil {
		return model.Energy1Min{}, false, err
	}
	if c.FwdNoData {
		return model.Energy1Min{}, false, nil
	}
	if !params.Valid() {
		return model.Energy1Min{}, false, fmt.Errorf("toEnergy1Min: meter params not ready")
	}
	return model.Energy1Min{Time: c.Time, KWh: params.ToKWh(c.Fwd), Raw: c.Fwd}, true, nil
}

func toEnergy30(edt []byte, params model.MeterParams, loc *time.Location) (model.Energy30Min, bool, error) {
	t, raw, noData, err := echonet.DecodeScheduled30(edt, loc)
	if err != nil {
		return model.Energy30Min{}, false, err
	}
	if noData {
		return model.Energy30Min{}, false, nil
	}
	if !params.Valid() {
		return model.Energy30Min{}, false, fmt.Errorf("toEnergy30: meter params not ready")
	}
	return model.Energy30Min{Time: t, KWh: params.ToKWh(raw), Raw: raw}, true, nil
}

func toStatus(edt []byte, now time.Time) (model.Status, error) {
	if len(edt) != 1 {
		return model.Status{}, fmt.Errorf("toStatus: expects 1 byte, got %d", len(edt))
	}
	return model.Status{Time: now, Fault: edt[0] == 0x41}, nil
}

func toMeta(f echonet.Frame, now time.Time) (model.Meta, model.MeterParams) {
	m := model.Meta{Time: now, Coefficient: 1}

	if edt, ok := f.EDT(echonet.EPCMakerCode); ok && len(edt) > 0 {
		m.MakerCode = "0x" + hex.EncodeToString(edt)
	}
	if edt, ok := f.EDT(echonet.EPCSerialNumber); ok && len(edt) > 0 {
		m.Serial = echonet.DecodeString(edt)
	}
	if edt, ok := f.EDT(echonet.EPCBRouteID); ok && len(edt) > 0 {
		if id, err := echonet.DecodeBRouteID(edt); err == nil {
			m.BRouteID = hex.EncodeToString(id.Raw)
		}
	}
	if edt, ok := f.EDT(echonet.EPCCoefficient); ok {
		if v, err := echonet.DecodeCoefficient(edt); err == nil {
			m.Coefficient = int(v)
		}
	}
	if edt, ok := f.EDT(echonet.EPCUnit); ok && len(edt) == 1 {
		m.UnitKWh = echonet.UnitKwh(edt[0])
	}
	if edt, ok := f.EDT(echonet.EPCDigits); ok {
		if d, err := echonet.DecodeDigits(edt); err == nil {
			m.Digits = d
		}
	}
	if edt, ok := f.EDT(echonet.EPCStandardVersion); ok && len(edt) > 0 {
		m.Version = "0x" + hex.EncodeToString(edt)
	}

	return m, model.MeterParams{Coefficient: m.Coefficient, UnitKWh: m.UnitKWh}
}
