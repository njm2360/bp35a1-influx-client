package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"main/internal/client"
	"main/internal/collector"
	"main/internal/config"
	"main/internal/storage"
	"main/internal/storage/influx"
	"main/internal/storage/stdout"
	"main/internal/transport"
	"main/internal/transport/bp35a1"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tr, err := newTransport(ctx, cfg)
	if err != nil {
		return err
	}
	defer tr.Close()

	store, err := newStore(cfg)
	if err != nil {
		return err
	}
	defer store.Close()

	cli := client.New(tr, log)
	col := collector.New(cli, store, cfg, log)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return cli.Run(ctx) })
	g.Go(func() error { return col.Run(ctx) })

	log.Info("smartmeter collector started", "meter", cfg.MeterTag, "output", cfg.Output)
	if err := g.Wait(); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

func newStore(cfg config.Config) (storage.Writer, error) {
	switch cfg.Output {
	case "stdout":
		return stdout.New(cfg), nil
	default:
		return influx.New(cfg)
	}
}

func newTransport(ctx context.Context, cfg config.Config) (transport.Transport, error) {
	return bp35a1.Open(ctx, bp35a1.Options{
		Port:      cfg.SerialPort,
		Baud:      cfg.SerialBaud,
		RouteBID:  cfg.BRouteID,
		Password:  cfg.BRoutePass,
		EpanCache: cfg.EpanCache,
		Logger:    slog.Default(),
	})
}

func logLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
