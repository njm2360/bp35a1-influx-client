package model

import "time"

type Power struct {
	Time        time.Time
	Watt        int32
	HasWatt     bool
	CurrentR    float64
	CurrentT    float64
	HasCurrentR bool
	HasCurrentT bool
}

func (p Power) HasData() bool {
	return p.HasWatt || p.HasCurrentR || p.HasCurrentT
}

type EnergyTotal struct {
	Time time.Time
	KWh  float64
	Raw  uint32
}

type Energy1Min struct {
	Time time.Time
	KWh  float64
	Raw  uint32
}

type Energy30Min struct {
	Time time.Time
	KWh  float64
	Raw  uint32
}

type Status struct {
	Time  time.Time
	Fault bool
}

type Meta struct {
	Time        time.Time
	MakerCode   string
	Serial      string
	BRouteID    string
	Coefficient int
	UnitKWh     float64
	Digits      int
	Version     string
}

type MeterParams struct {
	Coefficient int     // 0xD3 係数(未搭載時は 1)
	UnitKWh     float64 // 0xE1 単位係数(kWh)
}

func (p MeterParams) Valid() bool {
	return p.Coefficient > 0 && p.UnitKWh > 0
}

func (p MeterParams) ToKWh(raw uint32) float64 {
	return float64(raw) * float64(p.Coefficient) * p.UnitKWh
}
