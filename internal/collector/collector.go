package collector

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"main/internal/config"
	"main/internal/echonet"
	"main/internal/model"
	"main/internal/storage"
)

type Session interface {
	Get(ctx context.Context, epcs ...byte) (echonet.Frame, error)
	INF() <-chan echonet.Frame
}

type Collector struct {
	cli    Session
	out    storage.Writer
	cfg    config.Config
	log    *slog.Logger
	params atomic.Pointer[model.MeterParams]
}

func New(cli Session, out storage.Writer, cfg config.Config, log *slog.Logger) *Collector {
	return &Collector{cli: cli, out: out, cfg: cfg, log: log}
}

func (c *Collector) Run(ctx context.Context) error {
	if err := c.refreshMeta(ctx); err != nil {
		c.log.Warn("initial meta fetch failed; energy conversion deferred", "err", err)
	}

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return c.tick(ctx, c.cfg.PollPower, "power", c.pollPower) })
	g.Go(func() error { return c.tick(ctx, c.cfg.PollEnergy, "energy", c.pollEnergyMinute) })
	g.Go(func() error { return c.tick(ctx, time.Hour, "status", c.pollStatus) })
	g.Go(func() error { return c.tick(ctx, 24*time.Hour, "meta", c.refreshMeta) })
	g.Go(func() error { return c.tickAt(ctx, []int{5, 35}, "energy30", c.pollEnergy30) })
	g.Go(func() error { return c.handleINF(ctx) })
	return g.Wait()
}

func (c *Collector) tick(ctx context.Context, d time.Duration, name string, job func(context.Context) error) error {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := job(ctx); err != nil {
				c.log.Warn("job failed", "job", name, "err", err)
			}
		}
	}
}

func (c *Collector) tickAt(ctx context.Context, minutes []int, name string, job func(context.Context) error) error {
	for {
		next := nextMinuteMark(time.Now(), minutes)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			if err := job(ctx); err != nil {
				c.log.Warn("job failed", "job", name, "err", err)
			}
		}
	}
}

func nextMinuteMark(now time.Time, minutes []int) time.Time {
	for h := 0; h <= 1; h++ {
		base := now.Truncate(time.Hour).Add(time.Duration(h) * time.Hour)
		for _, m := range minutes {
			cand := base.Add(time.Duration(m) * time.Minute)
			if cand.After(now) {
				return cand
			}
		}
	}
	// フォールバック(通常到達しない)
	return now.Add(time.Minute)
}

func (c *Collector) handleINF(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f := <-c.cli.INF():
			c.handleINFFrame(ctx, f)
		}
	}
}

func (c *Collector) handleINFFrame(ctx context.Context, f echonet.Frame) {
	for _, p := range f.Props {
		switch p.EPC {
		case echonet.EPCScheduledFwd:
			if err := c.writeScheduled30(ctx, p.EDT); err != nil {
				c.log.Warn("inf energy30 failed", "err", err)
			}
		case echonet.EPCFaultStatus:
			if st, err := toStatus(p.EDT, time.Now()); err == nil {
				if err := c.out.WriteStatus(ctx, st); err != nil {
					c.log.Warn("inf status write failed", "err", err)
				}
			}
		}
	}
}

func (c *Collector) loadParams() model.MeterParams {
	if p := c.params.Load(); p != nil {
		return *p
	}
	return model.MeterParams{}
}

func (c *Collector) writeScheduled30(ctx context.Context, edt []byte) error {
	t, raw, noData, err := echonet.DecodeScheduled30(edt, c.cfg.Location)
	if err != nil {
		return err
	}
	if noData {
		return nil
	}
	p := c.loadParams()
	if !p.Valid() {
		return fmt.Errorf("writeScheduled30: meter params not ready")
	}
	return c.out.WriteEnergy30Min(ctx, model.Energy30Min{Time: t, KWh: p.ToKWh(raw), Raw: raw})
}
