// Command gophkeeper-server runs the GophKeeper server: the gRPC API that keeps
// the encrypted secrets of the users and syncs them between their devices,
// and the REST gateway with the Swagger description of the API.
//
// The flags and environment variables are described in
// internal/server/config; -h prints them.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/OneDayX/gophkeeper/internal/server/app"
	"github.com/OneDayX/gophkeeper/internal/server/config"
	"go.uber.org/zap"
)

// Build info, set at build time with
// -ldflags "-X main.buildVersion=... -X main.buildDate=... -X main.buildCommit=...".
var (
	buildVersion = "N/A"
	buildDate    = "N/A"
	buildCommit  = "N/A"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}

	// ctx is cancelled on SIGINT/SIGTERM so we can finish the calls in
	// progress before exiting.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger, err := zap.NewProduction()
	if err != nil {
		return err
	}
	// Sync often fails on stdout, and there is nowhere to report it anyway.
	defer func() { _ = logger.Sync() }()

	logger.Info("GophKeeper server",
		zap.String("version", buildVersion),
		zap.String("date", buildDate),
		zap.String("commit", buildCommit),
		zap.Bool("tls", cfg.TLSEnabled()),
	)

	a, err := app.New(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer a.Close()

	return a.Run(ctx)
}
