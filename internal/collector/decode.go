package collector

import (
	"encoding/hex"
	"fmt"
	"time"

	"main/internal/echonet"
	"main/internal/model"
)

func findEDT(f echonet.Frame, epc byte) ([]byte, bool) {
	for _, p := range f.Props {
		if p.EPC == epc {
			return p.EDT, true
		}
	}
	return nil, false
}

func toPower(f echonet.Frame, now time.Time) (model.Power, error) {
	out := model.Power{Time: now}

	edt, ok := findEDT(f, echonet.EPCInstantPower)
	if !ok {
		return model.Power{}, fmt.Errorf("toPower: missing EPC 0xE7")
	}
	w, noData, err := echonet.DecodeS32(edt)
	if err != nil {
		return model.Power{}, err
	}
	out.Watt, out.HasWatt = w, !noData

	if edt, ok := findEDT(f, echonet.EPCInstantCurrent); ok {
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
	raw, noData, err := echonet.DecodeU32(edt)
	if err != nil {
		return 0, 0, false, err
	}
	if noData {
		return 0, 0, false, nil
	}
	if !p.Valid() {
		return 0, raw, false, fmt.Errorf("toEnergy: meter params not ready")
	}
	return p.ToKWh(raw), raw, true, nil
}

func toStatus(edt []byte, now time.Time) (model.Status, error) {
	if len(edt) != 1 {
		return model.Status{}, fmt.Errorf("toStatus: expects 1 byte, got %d", len(edt))
	}
	return model.Status{Time: now, Fault: edt[0] == 0x41}, nil
}

func toMeta(f echonet.Frame, now time.Time) (model.Meta, model.MeterParams) {
	m := model.Meta{Time: now, Coefficient: 1}

	if edt, ok := findEDT(f, echonet.EPCMakerCode); ok && len(edt) > 0 {
		m.MakerCode = "0x" + hex.EncodeToString(edt)
	}
	if edt, ok := findEDT(f, echonet.EPCSerialNumber); ok && len(edt) > 0 {
		m.Serial = echonet.DecodeString(edt)
	}
	if edt, ok := findEDT(f, echonet.EPCBRouteID); ok && len(edt) > 0 {
		m.BRouteID = hex.EncodeToString(edt)
	}
	if edt, ok := findEDT(f, echonet.EPCCoefficient); ok {
		if v, noData, err := echonet.DecodeU32(edt); err == nil && !noData {
			m.Coefficient = int(v)
		}
	}
	if edt, ok := findEDT(f, echonet.EPCUnit); ok && len(edt) == 1 {
		m.UnitKWh = echonet.UnitKwh(edt[0])
	}
	if edt, ok := findEDT(f, echonet.EPCDigits); ok && len(edt) == 1 {
		m.Digits = int(edt[0])
	}
	if edt, ok := findEDT(f, echonet.EPCStandardVersion); ok && len(edt) > 0 {
		m.Version = "0x" + hex.EncodeToString(edt)
	}

	return m, model.MeterParams{Coefficient: m.Coefficient, UnitKWh: m.UnitKWh}
}
