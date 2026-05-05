package observability

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

type ctxKey string

const (
	ctxKeyRequestID ctxKey = "request_id"
	ctxKeyTenant    ctxKey = "tenant"
)

var serviceName = "bridge"

// Init configures the global zerolog logger to emit JSON to stdout.
func Init(level, service string) {
	if service != "" {
		serviceName = service
	}
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.TimestampFieldName = "ts"
	zerolog.LevelFieldName = "level"
	zerolog.MessageFieldName = "msg"

	zerolog.SetGlobalLevel(parseLevel(level))

	var w io.Writer = os.Stdout
	logger := zerolog.New(w).With().
		Timestamp().
		Str("service", serviceName).
		Logger()
	zerolog.DefaultContextLogger = &logger
}

func parseLevel(s string) zerolog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return zerolog.DebugLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	default:
		return zerolog.InfoLevel
	}
}

// FromContext returns a logger enriched with request_id and tenant if set.
// Falls back to a no-op (io.Discard) logger when neither the context nor
// zerolog.DefaultContextLogger has been initialized — guards against panics
// in code paths (notably tests) that do not call Init().
func FromContext(ctx context.Context) *zerolog.Logger {
	base := zerolog.Ctx(ctx)
	if base == nil || base.GetLevel() == zerolog.Disabled {
		if zerolog.DefaultContextLogger != nil {
			base = zerolog.DefaultContextLogger
		} else {
			fallback := zerolog.New(io.Discard)
			base = &fallback
		}
	}
	bld := base.With().Str("service", serviceName)
	if rid, ok := ctx.Value(ctxKeyRequestID).(string); ok && rid != "" {
		bld = bld.Str("request_id", rid)
	}
	if t, ok := ctx.Value(ctxKeyTenant).(string); ok && t != "" {
		bld = bld.Str("tenant", t)
	}
	l := bld.Logger()
	return &l
}

// WithRequestID stamps a request id onto the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// WithTenant stamps a tenant slug onto the context.
func WithTenant(ctx context.Context, slug string) context.Context {
	return context.WithValue(ctx, ctxKeyTenant, slug)
}

// RequestID returns the request id stored in ctx, or empty string.
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

// Tenant returns the tenant slug stored in ctx, or empty string.
func Tenant(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyTenant).(string); ok {
		return v
	}
	return ""
}

// Audit emits a structured audit event log.
// Persistence to audit_events is fire-and-forget through repo.LogAudit (called by handler/worker).
func Audit(ctx context.Context, kind string, ok bool, fields map[string]any) {
	l := FromContext(ctx)
	ev := l.Info().Str("kind", kind).Bool("ok", ok)
	for k, v := range fields {
		ev = ev.Interface(k, v)
	}
	ev.Msg("audit")
}
