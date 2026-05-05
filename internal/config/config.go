package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config groups all runtime configuration.
type Config struct {
	Service    string
	LogLevel   string
	HTTPAddr   string
	DatabaseURL string
	RedisAddr   string
	RedisDB     int
	RedisPassword string

	MasterKeyB64 string

	MigrateMode string // "auto" | "manual"

	HTTP HTTPConfig
}

type HTTPConfig struct {
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	MaxBodyBytes      int64
	ShutdownTimeout   time.Duration
}

const (
	envServiceName    = "SERVICE_NAME"
	envLogLevel       = "LOG_LEVEL"
	envHTTPAddr       = "HTTP_ADDR"
	envDatabaseURL    = "DATABASE_URL"
	envRedisAddr      = "REDIS_ADDR"
	envRedisDB        = "REDIS_DB"
	envRedisPassword  = "REDIS_PASSWORD"
	envMasterKey      = "MASTER_KEY"
	envMigrateMode    = "MIGRATE_MODE"
)

// FromEnv loads configuration from environment variables, applying sane defaults.
func FromEnv() (*Config, error) {
	cfg := &Config{
		Service:       getenv(envServiceName, "bridge"),
		LogLevel:      getenv(envLogLevel, "info"),
		HTTPAddr:      getenv(envHTTPAddr, ":8080"),
		DatabaseURL:   os.Getenv(envDatabaseURL),
		RedisAddr:     getenv(envRedisAddr, "127.0.0.1:6379"),
		RedisPassword: os.Getenv(envRedisPassword),
		MasterKeyB64:  os.Getenv(envMasterKey),
		MigrateMode:   getenv(envMigrateMode, "manual"),
		HTTP: HTTPConfig{
			ReadTimeout:       5 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
			ReadHeaderTimeout: 3 * time.Second,
			MaxBodyBytes:      1 << 20, // 1 MiB
			ShutdownTimeout:   15 * time.Second,
		},
	}
	if v := os.Getenv(envRedisDB); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("config: REDIS_DB invalid: %w", err)
		}
		cfg.RedisDB = n
	}

	if cfg.DatabaseURL == "" {
		return nil, errors.New("config: DATABASE_URL is required")
	}
	if cfg.MasterKeyB64 == "" {
		return nil, errors.New("config: MASTER_KEY is required")
	}
	if cfg.MigrateMode != "auto" && cfg.MigrateMode != "manual" {
		return nil, fmt.Errorf("config: MIGRATE_MODE must be auto|manual, got %q", cfg.MigrateMode)
	}
	return cfg, nil
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
