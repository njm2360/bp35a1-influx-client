package storage

import (
	"context"

	"github.com/njm2360/bp35a1-influx-client/internal/model"
)

type Writer interface {
	WritePower(ctx context.Context, m model.Power) error
	WriteEnergyTotal(ctx context.Context, m model.EnergyTotal) error
	WriteEnergy1Min(ctx context.Context, m model.Energy1Min) error
	WriteEnergy30Min(ctx context.Context, m model.Energy30Min) error
	WriteStatus(ctx context.Context, m model.Status) error
	WriteMeta(ctx context.Context, m model.Meta) error
	Close() error
}
