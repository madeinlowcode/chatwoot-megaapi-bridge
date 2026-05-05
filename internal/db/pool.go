package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/db/migrations"
)

var embeddedMigrations fs.FS = migrations.FS

// NewPool creates a configured pgx connection pool.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}
	cfg.MaxConns = 50
	cfg.MinConns = 5
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// MigrateUp runs goose Up against the embedded migrations.
func MigrateUp(ctx context.Context, dsn string) error {
	if dsn == "" {
		return errors.New("db: empty dsn")
	}
	goose.SetBaseFS(embeddedMigrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("db: dialect: %w", err)
	}
	sqlDB, err := goose.OpenDBWithDriver("postgres", dsn)
	if err != nil {
		return fmt.Errorf("db: open: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	return goose.UpContext(ctx, sqlDB, ".")
}

// MigrateDown rolls back the latest migration.
func MigrateDown(ctx context.Context, dsn string) error {
	goose.SetBaseFS(embeddedMigrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	sqlDB, err := goose.OpenDBWithDriver("postgres", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	return goose.DownContext(ctx, sqlDB, ".")
}

// MigrateStatus prints the current goose status.
func MigrateStatus(ctx context.Context, dsn string) error {
	goose.SetBaseFS(embeddedMigrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	sqlDB, err := goose.OpenDBWithDriver("postgres", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	return goose.StatusContext(ctx, sqlDB, ".")
}
