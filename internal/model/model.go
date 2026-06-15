package model

import "time"

// 瞬時電力・電流(measurement "power")
type Power struct {
	Time        time.Time
	Watt        int32
	HasWatt     bool
	CurrentR    float64
	CurrentT    float64
	HasCurrentR bool
	HasCurrentT bool
}

// 書き込むべき計測値が一つでもあるか(全て欠測なら false)
func (p Power) HasData() bool {
	return p.HasWatt || p.HasCurrentR || p.HasCurrentT
}

// 積算電力量現在値(measurement "energy_total", EPC 0xE0)
type EnergyTotal struct {
	Time time.Time
	KWh  float64
	Raw  uint32
}

// 1分積算電力量(measurement "energy_1min", EPC 0xD0 正方向)
type Energy1Min struct {
	Time time.Time
	KWh  float64
	Raw  uint32
}

// 定時積算電力量 30分値(measurement "energy_30min", EPC 0xEA)
type Energy30Min struct {
	Time time.Time
	KWh  float64
	Raw  uint32
}

// 異常発生状態(measurement "status", EPC 0x88)
type Status struct {
	Time  time.Time
	Fault bool
}

// メータの静的属性(measurement "meta")
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

// 積算値の kWh 換算に必要なパラメータ
// collector が起動時/日次の meta 取得で更新し、各積算 decode で参照する。
type MeterParams struct {
	Coefficient int     // 0xD3 係数(未搭載時は 1)
	UnitKWh     float64 // 0xE1 単位係数(kWh)
}

// 換算可能なパラメータが揃っているかを返す
func (p MeterParams) Valid() bool {
	return p.Coefficient > 0 && p.UnitKWh > 0
}

// 生の積算カウンタを kWh へ換算する
func (p MeterParams) ToKWh(raw uint32) float64 {
	return float64(raw) * float64(p.Coefficient) * p.UnitKWh
}
