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

func (c *Collector) pollEnergyMinute(ctx context.Context) error {
	params := c.loadParams()
	var errs []error
	if c.supportsEPC(echonet.EPCCumulativeFwd) {
		errs = append(errs, c.pollEnergyTotal(ctx, params))
	}
	if c.supportsEPC(echonet.EPCCumulative1Min) {
		errs = append(errs, c.pollEnergy1Min(ctx, params))
	}
	return errors.Join(errs...)
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

// 0xD0 1分積算電力量計測値。正方向値と埋め込み計測時刻を使う。
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

	return c.out.WriteMeta(ctx, meta)
}

func (c *Collector) fetchPropertyMap(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.GetTimeoutLong)
	defer cancel()

	f, err := c.cli.Get(ctx, echonet.EPCGetPropertyMap)
	if err != nil {
		return fmt.Errorf("get 0x9F: %w", err)
	}
	edt, ok := findEDT(f, echonet.EPCGetPropertyMap)
	if !ok || len(edt) == 0 {
		return fmt.Errorf("0x9F not returned")
	}
	epcs, err := echonet.DecodePropertyMap(edt)
	if err != nil {
		return fmt.Errorf("decode 0x9F: %w", err)
	}

	c.propMap = make(map[byte]struct{}, len(epcs))
	for _, e := range epcs {
		c.propMap[e] = struct{}{}
	}

	list := make([]string, len(epcs))
	for i, e := range epcs {
		list[i] = fmt.Sprintf("0x%02X", e)
	}
	c.log.Info("property map", "count", len(epcs), "epcs", strings.Join(list, ","))
	return nil
}
