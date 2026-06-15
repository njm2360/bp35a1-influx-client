package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"main/internal/echonet"
	"main/internal/model"
)

func (c *Collector) getPartial(ctx context.Context, epcs ...byte) (echonet.Frame, error) {
	f, err := c.cli.Get(ctx, epcs...)
	if err != nil {
		if f.ESV == echonet.ESVGetSNA && len(f.Props) > 0 {
			c.log.Warn("partial response; using available properties", "err", err)
			return f, nil
		}
		return echonet.Frame{}, err
	}
	return f, nil
}

// 瞬時電力・電流(0xE7,0xE8)を取得して書き込む
func (c *Collector) pollPower(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.GetTimeout)
	defer cancel()

	f, err := c.getPartial(ctx, echonet.EPCInstantPower, echonet.EPCInstantCurrent)
	if err != nil {
		return err
	}
	p, err := toPower(f, time.Now())
	if err != nil {
		return err
	}
	return c.out.WritePower(ctx, p)
}

// 積算現在値(0xE0)と 1分積算(0xD0 正方向)を取得して書き込む。
// 一部メータは複数EPCを1要求にまとめると応答しない(SNA で一部を空返し or 無応答)
// ため、E0 と D0 を別々の Get に分割する。
func (c *Collector) pollEnergyMinute(ctx context.Context) error {
	params := c.loadParams()
	return errors.Join(
		c.pollEnergyTotal(ctx, params),
		// 0xD0(1分積算)は本メータが未実装のため無効化。
		// c.pollEnergy1Min(ctx, params),
	)
}

// 0xE0 積算電力量計測値(正方向)
func (c *Collector) pollEnergyTotal(ctx context.Context, params model.MeterParams) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.GetTimeout)
	defer cancel()

	f, err := c.getPartial(ctx, echonet.EPCCumulativeFwd)
	if err != nil {
		return err
	}
	if edt, ok := findEDT(f, echonet.EPCCumulativeFwd); ok {
		if kwh, raw, ok, err := toEnergy(edt, params); err != nil {
			c.log.Warn("energy_total decode", "err", err)
		} else if ok {
			if err := c.out.WriteEnergyTotal(ctx, model.EnergyTotal{Time: time.Now(), KWh: kwh, Raw: raw}); err != nil {
				return err
			}
		}
	}
	return nil
}

// 0xD0 1分積算電力量計測値。15byte(年月日+時分秒+正方向4+逆方向4)。
// 正方向値と埋め込み計測時刻を使う。
// 本メータは 0xD0 未実装のため pollEnergyMinute からの呼び出しは無効化済み。
// 対応メータでは有効化して使える。
func (c *Collector) pollEnergy1Min(ctx context.Context, params model.MeterParams) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.GetTimeout)
	defer cancel()

	f, err := c.getPartial(ctx, echonet.EPCCumulative1Min)
	if err != nil {
		return err
	}
	edt, ok := findEDT(f, echonet.EPCCumulative1Min)
	if !ok || len(edt) == 0 {
		c.log.Warn("energy_1min: 0xD0 not returned")
		return nil
	}
	t, raw, noData, err := echonet.DecodeCumulative1Min(edt, c.cfg.Location)
	switch {
	case err != nil:
		c.log.Warn("energy_1min decode", "err", err)
		return nil
	case noData:
		return nil // 欠測。書き込まない。
	case !params.Valid():
		c.log.Warn("energy_1min: meter params not ready")
		return nil
	default:
		return c.out.WriteEnergy1Min(ctx, model.Energy1Min{Time: t, KWh: params.ToKWh(raw), Raw: raw})
	}
}

func (c *Collector) pollStatus(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.GetTimeout)
	defer cancel()

	f, err := c.cli.Get(ctx, echonet.EPCFaultStatus)
	if err != nil {
		return err
	}
	edt, ok := findEDT(f, echonet.EPCFaultStatus)
	if !ok {
		return fmt.Errorf("pollStatus: missing EPC 0x88")
	}
	st, err := toStatus(edt, time.Now())
	if err != nil {
		return err
	}
	return c.out.WriteStatus(ctx, st)
}

func (c *Collector) pollEnergy30(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.GetTimeout)
	defer cancel()

	f, err := c.cli.Get(ctx, echonet.EPCScheduledFwd)
	if err != nil {
		return err
	}
	edt, ok := findEDT(f, echonet.EPCScheduledFwd)
	if !ok {
		return fmt.Errorf("pollEnergy30: missing EPC 0xEA")
	}
	return c.writeScheduled30(ctx, edt)
}

func (c *Collector) refreshMeta(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.GetTimeoutLong)
	defer cancel()

	f, err := c.getPartial(ctx,
		echonet.EPCMakerCode, echonet.EPCSerialNumber, echonet.EPCBRouteID,
		echonet.EPCCoefficient, echonet.EPCUnit, echonet.EPCDigits, echonet.EPCStandardVersion,
	)
	if err != nil {
		return err
	}
	meta, params := toMeta(f, time.Now())
	c.params.Store(&params)
	if !params.Valid() {
		c.log.Warn("meter params incomplete", "coefficient", params.Coefficient, "unit_kwh", params.UnitKWh)
	}

	c.logGetPropertyMap(ctx)

	return c.out.WriteMeta(ctx, meta)
}

func (c *Collector) logGetPropertyMap(ctx context.Context) {
	f, err := c.getPartial(ctx, echonet.EPCGetPropertyMap)
	if err != nil {
		c.log.Warn("get property map failed", "err", err)
		return
	}
	edt, ok := findEDT(f, echonet.EPCGetPropertyMap)
	if !ok || len(edt) == 0 {
		c.log.Warn("get property map: 0x9F not returned")
		return
	}
	epcs, err := echonet.DecodePropertyMap(edt)
	if err != nil {
		c.log.Warn("get property map decode", "err", err)
		return
	}
	list := make([]string, len(epcs))
	for i, e := range epcs {
		list[i] = fmt.Sprintf("0x%02X", e)
	}
	c.log.Info("GET property map",
		"count", len(epcs),
		"epcs", strings.Join(list, ","),
	)
}
