package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type Health struct {
	DB    *pgxpool.Pool
	Redis *redis.Client
}

// Healthz is a liveness check; always 200 if the process is alive.
func (h *Health) Healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// Readyz pings PG + Redis. Returns 503 if either is unreachable.
func (h *Health) Readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := map[string]string{}
	ok := true

	if err := h.DB.Ping(ctx); err != nil {
		checks["postgres"] = "down: " + err.Error()
		ok = false
	} else {
		checks["postgres"] = "up"
	}
	if err := h.Redis.Ping(ctx).Err(); err != nil {
		checks["redis"] = "down: " + err.Error()
		ok = false
	} else {
		checks["redis"] = "up"
	}

	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "checks": checks})
}
