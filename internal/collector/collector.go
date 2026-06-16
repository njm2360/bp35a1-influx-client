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

type Client interface {
	Get(ctx context.Context, epcs ...byte) (echonet.Frame, error)
	INF() <-chan echonet.Frame
}

type Collector struct {
	cli     Client
	out     storage.Writer
	cfg     config.Config
	log     *slog.Logger
	params  atomic.Pointer[model.MeterParams]
	propMap map[byte]struct{}
}

func (c *Collector) supportsEPC(epc byte) bool {
	_, ok := c.propMap[epc]
	return ok
}

func New(cli Client, out storage.Writer, cfg config.Config, log *slog.Logger) *Collector {
	return &Collector{cli: cli, out: out, cfg: cfg, log: log}
}

func (c *Collector) Run(ctx context.Context) error {
	if err := c.fetchPropertyMap(ctx); err != nil {
		return fmt.Errorf("property map: %w", err)
	}

	if err := c.refreshMeta(ctx); err != nil {
		c.log.Warn("initial meta fetch failed; energy conversion deferred", "err", err)
	}

	g, ctx := errgroup.WithContext(ctx)
	if c.supportsEPC(echonet.EPCInstantPower) {
		g.Go(func() error { return c.tick(ctx, c.cfg.PollPower, "power", c.pollPower) })
	}
	if c.supportsEPC(echonet.EPCCumulativeFwd) || c.supportsEPC(echonet.EPCCumulative1Min) {
		g.Go(func() error { return c.tick(ctx, c.cfg.PollEnergy, "energy", c.pollEnergyMinute) })
	}
	if c.supportsEPC(echonet.EPCFaultStatus) {
		g.Go(func() error { return c.tick(ctx, time.Hour, "status", c.pollStatus) })
	}
	g.Go(func() error { return c.tick(ctx, 24*time.Hour, "meta", c.refreshMeta) })
	if c.supportsEPC(echonet.EPCScheduledFwd) {
		g.Go(func() error { return c.tickAt(ctx, []int{5, 35}, "energy30", c.pollEnergy30) })
	}
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
			start := time.Now()
			if err := job(ctx); err != nil {
				c.log.Warn("job failed", "job", name, "err", err, "elapsed", time.Since(start))
			} else {
				c.log.Debug("job completed", "job", name, "elapsed", time.Since(start))
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
			start := time.Now()
			if err := job(ctx); err != nil {
				c.log.Warn("job failed", "job", name, "err", err, "elapsed", time.Since(start))
			} else {
				c.log.Debug("job completed", "job", name, "elapsed", time.Since(start))
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
	c.log.Debug("handling INF", "props", len(f.Props))
	for _, p := range f.Props {
		switch p.EPC {
		case echonet.EPCScheduledFwd:
			if err := c.writeScheduled30(ctx, p.EDT); err != nil {
				c.log.Warn("inf energy30 failed", "err", err)
			}
		case echonet.EPCFaultStatus:
			st, err := toStatus(p.EDT, time.Now())
			if err != nil {
				c.log.Warn("inf status decode failed", "err", err)
				continue
			}
			if err := c.out.WriteStatus(ctx, st); err != nil {
				c.log.Warn("inf status write failed", "err", err)
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
	m, ok, err := toEnergy30(edt, c.loadParams(), c.cfg.Location)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return c.out.WriteEnergy30Min(ctx, m)
}
