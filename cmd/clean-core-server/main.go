package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"znt/internal/app/config"
	"znt/internal/app/core"
	"znt/internal/app/logging"
	"znt/internal/bridge/array"
	"znt/internal/server"
)

func main() {
	var configPath string
	var showVersion bool
	var migrationCmd string
	var migrationDir string
	var migrationState string
	flag.StringVar(&configPath, "config", "", "optional json/yaml config path")
	flag.BoolVar(&showVersion, "version", false, "print service version and exit")
	flag.StringVar(&migrationCmd, "migration", "", "run migration command: up or status")
	flag.StringVar(&migrationDir, "migration-dir", "migrations", "migration directory")
	flag.StringVar(&migrationState, "migration-state", ".clean-core-migrations.json", "local migration state path")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	if showVersion {
		fmt.Println(cfg.Version)
		return
	}
	if migrationCmd != "" {
		if err := runMigrationCommand(migrationCmd, migrationDir, migrationState, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "migration error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	logger := logging.New(cfg.LogLevel)
	logger.Info("clean core starting", slog.String("version", cfg.Version), slog.String("addr", cfg.HTTPAddr))

	appCore, err := core.New(cfg)
	if err != nil {
		logger.Error("server init failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	handler := server.NewHandlerWithCore(appCore, logger)

	runtimeCtx, stopRuntime := context.WithCancel(context.Background())
	defer stopRuntime()
	if cfg.ExternalDeliveryRetryEnabled {
		worker := array.DeliveryRetryWorker{
			Bridge:      appCore.ArrayBridge,
			Logger:      logger,
			Interval:    time.Duration(cfg.ExternalDeliveryRetryIntervalSeconds) * time.Second,
			BatchSize:   cfg.ExternalDeliveryRetryBatchSize,
			MaxAttempts: cfg.ExternalDeliveryRetryMaxAttempts,
		}
		go worker.Run(runtimeCtx)
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: time.Duration(cfg.HTTPReadHeaderTimeoutSeconds) * time.Second,
		ReadTimeout:       time.Duration(cfg.HTTPReadTimeoutSeconds) * time.Second,
		WriteTimeout:      time.Duration(cfg.HTTPWriteTimeoutSeconds) * time.Second,
		IdleTimeout:       time.Duration(cfg.HTTPIdleTimeoutSeconds) * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-stop:
		logger.Info("shutdown signal received", slog.String("signal", sig.String()))
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}

	stopRuntime()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("clean core stopped")
}
