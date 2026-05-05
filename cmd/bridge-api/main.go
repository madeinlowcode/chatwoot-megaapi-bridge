package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/config"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/crypto"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/db"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/handler"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/observability"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/queue"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/repo"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/tenant"

	"net/http"
)

func main() {
	root := &cobra.Command{
		Use:   "bridge",
		Short: "Chatwoot ↔ megaAPI bridge",
	}
	root.AddCommand(serveCmd(), migrateCmd(), tenantsCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd.Context())
		},
	}
}

func runServe(parent context.Context) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	observability.Init(cfg.LogLevel, "bridge-api")

	if cfg.MigrateMode == "auto" {
		if err := db.MigrateUp(parent, cfg.DatabaseURL); err != nil {
			return fmt.Errorf("auto-migrate: %w", err)
		}
	}

	pool, err := db.NewPool(parent, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer func() { _ = rdb.Close() }()

	ks, err := crypto.LoadKeystoreFromEnv()
	if err != nil {
		return err
	}

	queries := repo.New(pool)
	cache := tenant.New(queries, ks, 5*time.Minute, 10_000)

	asynqClient := queue.NewClient(asynq.RedisClientOpt{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer func() { _ = asynqClient.Close() }()

	routes := &handler.Routes{
		Health: &handler.Health{DB: pool, Redis: rdb},
		Megaapi: &handler.MegaapiWebhook{
			Tenants:  cache,
			Queries:  queries,
			Enqueuer: asynqClient,
			MaxBody:  cfg.HTTP.MaxBodyBytes,
		},
		Chatwoot: &handler.ChatwootWebhook{
			Tenants:  cache,
			Queries:  queries,
			Enqueuer: asynqClient,
			MaxBody:  cfg.HTTP.MaxBodyBytes,
		},
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           routes.Build(),
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
	}

	ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		log := observability.FromContext(ctx)
		log.Info().Str("addr", cfg.HTTPAddr).Str("kind", "service.starting").Msg("listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel2 := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel2()
	return srv.Shutdown(shutdownCtx)
}

func migrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run database migrations",
	}
	up := &cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.FromEnv()
			if err != nil {
				return err
			}
			observability.Init(cfg.LogLevel, "bridge-migrate")
			return db.MigrateUp(context.Background(), cfg.DatabaseURL)
		},
	}
	down := &cobra.Command{
		Use:   "down",
		Short: "Roll back the last migration",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.FromEnv()
			if err != nil {
				return err
			}
			observability.Init(cfg.LogLevel, "bridge-migrate")
			return db.MigrateDown(context.Background(), cfg.DatabaseURL)
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show migration status",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.FromEnv()
			if err != nil {
				return err
			}
			observability.Init(cfg.LogLevel, "bridge-migrate")
			return db.MigrateStatus(context.Background(), cfg.DatabaseURL)
		},
	}
	cmd.AddCommand(up, down, status)
	return cmd
}
