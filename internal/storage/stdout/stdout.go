package stdout

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"main/internal/config"
	"main/internal/model"
	"main/internal/storage"
)

type Writer struct {
	mu    sync.Mutex
	w     io.Writer
	enc   *json.Encoder
	meter string
}

var _ storage.Writer = (*Writer)(nil)

func New(cfg config.Config) *Writer {
	w := os.Stdout
	return &Writer{w: w, enc: json.NewEncoder(w), meter: cfg.MeterTag}
}

func (w *Writer) write(measurement string, t any, fields map[string]any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec := map[string]any{
		"measurement": measurement,
		"meter":       w.meter,
		"time":        t,
	}
	for k, v := range fields {
		rec[k] = v
	}
	if err := w.enc.Encode(rec); err != nil {
		return fmt.Errorf("stdout: encode %s: %w", measurement, err)
	}
	return nil
}

func (w *Writer) WritePower(ctx context.Context, m model.Power) error {
	if !m.HasData() {
		return nil
	}
	fields := map[string]any{}
	if m.HasWatt {
		fields["power_w"] = m.Watt
	}
	if m.HasCurrentR {
		fields["current_r"] = m.CurrentR
	}
	if m.HasCurrentT {
		fields["current_t"] = m.CurrentT
	}
	return w.write("power", m.Time, fields)
}

func (w *Writer) WriteEnergyTotal(ctx context.Context, m model.EnergyTotal) error {
	return w.write("energy_total", m.Time, map[string]any{"kwh": m.KWh, "raw": m.Raw})
}

func (w *Writer) WriteEnergy1Min(ctx context.Context, m model.Energy1Min) error {
	return w.write("energy_1min", m.Time, map[string]any{"kwh": m.KWh, "raw": m.Raw})
}

func (w *Writer) WriteEnergy30Min(ctx context.Context, m model.Energy30Min) error {
	return w.write("energy_30min", m.Time, map[string]any{"kwh": m.KWh, "raw": m.Raw})
}

func (w *Writer) WriteStatus(ctx context.Context, m model.Status) error {
	return w.write("status", m.Time, map[string]any{"fault": m.Fault})
}

func (w *Writer) WriteMeta(ctx context.Context, m model.Meta) error {
	return w.write("meta", m.Time, map[string]any{
		"maker_code":  m.MakerCode,
		"serial":      m.Serial,
		"broute_id":   m.BRouteID,
		"coefficient": m.Coefficient,
		"unit_kwh":    m.UnitKWh,
		"digits":      m.Digits,
		"version":     m.Version,
	})
}

func (w *Writer) Close() error { return nil }
