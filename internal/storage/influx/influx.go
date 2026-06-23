package influx

import (
	"context"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/influxdata/influxdb-client-go/v2/api/write"

	"main/internal/config"
	"main/internal/model"
	"main/internal/storage"
)

type Writer struct {
	client   influxdb2.Client
	write    api.WriteAPIBlocking
	meterTag map[string]string
	timeout  time.Duration
}

var _ storage.Writer = (*Writer)(nil)

func New(cfg config.Config) (*Writer, error) {
	client := influxdb2.NewClient(cfg.InfluxURL, cfg.InfluxToken)
	return &Writer{
		client:   client,
		write:    client.WriteAPIBlocking(cfg.InfluxOrg, cfg.InfluxBucket),
		meterTag: map[string]string{"meter": cfg.MeterTag},
		timeout:  cfg.WriteTimeout,
	}, nil
}

func (w *Writer) writePoint(ctx context.Context, p *write.Point) error {
	if _, ok := ctx.Deadline(); !ok && w.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, w.timeout)
		defer cancel()
	}
	return w.write.WritePoint(ctx, p)
}

func (w *Writer) WritePower(ctx context.Context, m model.Power) error {
	if !m.HasData() {
		// 全て欠測。フィールドなしの Point は InfluxDB が拒否するため書き込まない。
		return nil
	}
	fields := map[string]any{}
	if m.HasWatt {
		fields["power_w"] = int64(m.Watt)
	}
	if m.HasCurrentR {
		fields["current_r"] = m.CurrentR
	}
	if m.HasCurrentT {
		fields["current_t"] = m.CurrentT
	}
	p := influxdb2.NewPoint("power", w.meterTag, fields, m.Time)
	return w.writePoint(ctx, p)
}

func (w *Writer) WriteEnergyTotal(ctx context.Context, m model.EnergyTotal) error {
	p := influxdb2.NewPoint("energy_total", w.meterTag, map[string]any{
		"kwh": m.KWh,
		"raw": int64(m.Raw),
	}, m.Time)
	return w.writePoint(ctx, p)
}

func (w *Writer) WriteEnergy1Min(ctx context.Context, m model.Energy1Min) error {
	p := influxdb2.NewPoint("energy_1min", w.meterTag, map[string]any{
		"kwh": m.KWh,
		"raw": int64(m.Raw),
	}, m.Time)
	return w.writePoint(ctx, p)
}

func (w *Writer) WriteEnergy30Min(ctx context.Context, m model.Energy30Min) error {
	p := influxdb2.NewPoint("energy_30min", w.meterTag, map[string]any{
		"kwh": m.KWh,
		"raw": int64(m.Raw),
	}, m.Time)
	return w.writePoint(ctx, p)
}

func (w *Writer) WriteStatus(ctx context.Context, m model.Status) error {
	p := influxdb2.NewPoint("status", w.meterTag, map[string]any{
		"fault": m.Fault,
	}, m.Time)
	return w.writePoint(ctx, p)
}

func (w *Writer) WriteMeta(ctx context.Context, m model.Meta) error {
	p := influxdb2.NewPoint("meta", w.meterTag, map[string]any{
		"maker_code":  m.MakerCode,
		"serial":      m.Serial,
		"broute_id":   m.BRouteID,
		"coefficient": int64(m.Coefficient),
		"unit_kwh":    m.UnitKWh,
		"digits":      int64(m.Digits),
		"version":     m.Version,
	}, m.Time)
	return w.writePoint(ctx, p)
}

func (w *Writer) Close() error {
	w.client.Close()
	return nil
}
