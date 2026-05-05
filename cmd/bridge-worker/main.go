package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/chatwoot"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/config"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/crypto"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/db"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/megaapi"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/observability"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/queue"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/repo"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/tenant"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	observability.Init(cfg.LogLevel, "bridge-worker")

	parent, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := db.NewPool(parent, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	ks, err := crypto.LoadKeystoreFromEnv()
	if err != nil {
		return err
	}
	queries := repo.New(pool)
	cache := tenant.New(queries, ks, 5*time.Minute, 10_000)

	mega := megaapi.New()
	cw := chatwoot.New()

	asynqSrv := asynq.NewServer(
		asynq.RedisClientOpt{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
			DB:       cfg.RedisDB,
		},
		asynq.Config{
			Concurrency: 100,
			Queues: map[string]int{
				queue.QueueWAtoCW: 5,
				queue.QueueCWtoWA: 5,
			},
			RetryDelayFunc: func(n int, _ error, _ *asynq.Task) time.Duration {
				return retryBackoff(n)
			},
		},
	)

	mux := asynq.NewServeMux()

	wa := &worker.WAtoCW{Tenants: cache, Queries: queries, Chatwoot: cw}
	cwh := &worker.CWtoWA{Tenants: cache, Queries: queries, Megaapi: mega}
	mux.HandleFunc(queue.TaskWAtoCW, wa.HandleTask)
	mux.HandleFunc(queue.TaskCWtoWA, cwh.HandleTask)

	errCh := make(chan error, 1)
	go func() {
		log := observability.FromContext(parent)
		log.Info().Str("kind", "service.starting").Msg("worker listening")
		if err := asynqSrv.Run(mux); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-parent.Done():
		asynqSrv.Shutdown()
		return nil
	case err := <-errCh:
		return err
	}
}

// retryBackoff implements docs/07's exponential schedule (1s..10min).
func retryBackoff(attempt int) time.Duration {
	switch attempt {
	case 0, 1:
		return 1 * time.Second
	case 2:
		return 2 * time.Second
	case 3:
		return 4 * time.Second
	case 4:
		return 8 * time.Second
	case 5:
		return 30 * time.Second
	case 6:
		return 2 * time.Minute
	default:
		return 10 * time.Minute
	}
}
