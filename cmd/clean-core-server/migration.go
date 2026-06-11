package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"znt/internal/app/config"
	"znt/internal/storage/migration"
	"znt/internal/storage/postgres"
)

func runMigrationCommand(command string, dir string, statePath string, cfg config.Config) error {
	migrations, err := migration.LoadDir(dir)
	if err != nil {
		return err
	}
	if manifest, ok, err := migration.LoadManifestIfExists(migration.DefaultManifestPath(dir)); err != nil {
		return err
	} else if ok {
		if err := migration.ValidateManifest(migrations, manifest); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var store migration.Store
	var executor migration.Executor
	var db *sql.DB
	databaseURL := cfg.DatabaseURL
	if databaseURL != "" {
		db, err = postgres.Open(ctx, databaseURL, postgres.PoolConfig{
			MaxOpenConns:    cfg.DBMaxOpenConns,
			MaxIdleConns:    cfg.DBMaxIdleConns,
			ConnMaxLifetime: time.Duration(cfg.DBConnMaxLifetimeSeconds) * time.Second,
			ConnMaxIdleTime: time.Duration(cfg.DBConnMaxIdleTimeSeconds) * time.Second,
		})
		if err != nil {
			return err
		}
		defer db.Close()
		store = postgres.NewRepositories(db).Migrations
		executor = migration.SQLExecutor{DB: db}
	} else {
		store = migration.NewFileStore(statePath)
	}
	runner := migration.NewRunner(store)
	switch command {
	case "up":
		var applied []migration.AppliedMigration
		if executor != nil {
			applied, err = runner.UpWithExecutor(ctx, migrations, executor)
		} else {
			applied, err = runner.Up(ctx, migrations)
		}
		if err != nil {
			return err
		}
		fmt.Printf("applied=%d\n", len(applied))
	case "status":
		applied, err := runner.Status(ctx, migrations)
		if err != nil {
			return err
		}
		fmt.Printf("applied=%d total=%d\n", len(applied), len(migrations))
		if databaseURL != "" {
			report, err := migration.ValidateLiveSchema(ctx, migration.PostgresInspector{DB: db}, migrations)
			if err != nil {
				return err
			}
			fmt.Printf("live_schema=%s\n", report.Status)
			if report.Status != "ready" {
				fmt.Printf("live_schema_details=%s\n", report.Details())
			}
		}
	default:
		return fmt.Errorf("unsupported migration command %q", command)
	}
	return nil
}
