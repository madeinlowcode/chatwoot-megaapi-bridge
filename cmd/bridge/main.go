package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/bridge"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/migrations"
)

const usage = `bridge — chatwoot-megaapi-bridge

Usage:
  bridge serve         Run the HTTP server and worker pools
  bridge migrate       Apply embedded migrations
  bridge tenant add    Register a new tenant
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	log := newLogger()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cmd := os.Args[1]
	args := os.Args[2:]
	if err := dispatch(ctx, log, cmd, args); err != nil {
		log.Fatal().Err(err).Msg("command failed")
	}
}

func dispatch(ctx context.Context, log zerolog.Logger, cmd string, args []string) error {
	switch cmd {
	case "serve":
		return cmdServe(ctx, log)
	case "migrate":
		return cmdMigrate(ctx, log)
	case "tenant":
		return cmdTenant(ctx, log, args)
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func newLogger() zerolog.Logger {
	level := strings.ToLower(getEnv("LOG_LEVEL", "info"))
	lv, err := zerolog.ParseLevel(level)
	if err != nil {
		lv = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lv)
	return zerolog.New(os.Stdout).With().Timestamp().Logger()
}

func getEnv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func loadMasterKey() ([]byte, error) {
	raw := os.Getenv("MASTER_KEY")
	if raw == "" {
		return nil, errors.New("MASTER_KEY env var is required (32 bytes base64)")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("MASTER_KEY base64 decode: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("MASTER_KEY must decode to 32 bytes, got %d", len(key))
	}
	return key, nil
}

func loadDSN() (string, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return "", errors.New("DATABASE_URL env var is required")
	}
	return dsn, nil
}

func cmdServe(ctx context.Context, log zerolog.Logger) error {
	key, dsn, err := loadKeyAndDSN()
	if err != nil {
		return err
	}
	db, err := bridge.NewDB(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	srv := bridge.NewServer(db, key, bridge.Config{}, log)
	if err := srv.RecoverPending(ctx); err != nil {
		log.Warn().Err(err).Msg("recover pending failed")
	}
	go srv.RunWorkers(ctx)
	return runHTTP(ctx, log, srv)
}

func runHTTP(ctx context.Context, log zerolog.Logger, srv *bridge.Server) error {
	addr := ":" + getEnv("BRIDGE_PORT", "8080")
	httpSrv := &http.Server{Addr: addr, Handler: srv.Routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(c)
	}()
	log.Info().Str("addr", addr).Msg("listening")
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func cmdMigrate(ctx context.Context, log zerolog.Logger) error {
	dsn, err := loadDSN()
	if err != nil {
		return err
	}
	db, err := bridge.NewDB(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	files, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return err
	}
	return applyMigrations(ctx, db, log, files)
}

func applyMigrations(ctx context.Context, db *bridge.DB, log zerolog.Logger, files []fs.DirEntry) error {
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".sql") {
			continue
		}
		body, err := migrations.FS.ReadFile(f.Name())
		if err != nil {
			return err
		}
		if _, err := db.Pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", f.Name(), err)
		}
		log.Info().Str("file", f.Name()).Msg("migration applied")
	}
	return nil
}

func cmdTenant(ctx context.Context, log zerolog.Logger, args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return errors.New("usage: bridge tenant add --slug ... --megaapi-host ... etc.")
	}
	return cmdTenantAdd(ctx, log, args[1:])
}

type tenantFlags struct {
	slug, megaHost, megaInstance, megaToken string
	cwURL, cwToken                          string
	cwAccount, cwInbox                      int
	skipReachCheck                          bool
}

func parseTenantFlags(args []string) (tenantFlags, error) {
	fs := flag.NewFlagSet("tenant add", flag.ContinueOnError)
	var f tenantFlags
	fs.StringVar(&f.slug, "slug", "", "tenant slug (lowercase, dashes)")
	fs.StringVar(&f.megaHost, "megaapi-host", "", "megaAPI base URL")
	fs.StringVar(&f.megaInstance, "megaapi-instance", "", "megaAPI instance ID")
	fs.StringVar(&f.megaToken, "megaapi-token", "", "megaAPI bearer token")
	fs.StringVar(&f.cwURL, "chatwoot-url", "", "Chatwoot base URL")
	fs.StringVar(&f.cwToken, "chatwoot-token", "", "Chatwoot api_access_token")
	fs.IntVar(&f.cwAccount, "chatwoot-account", 0, "Chatwoot account id")
	fs.IntVar(&f.cwInbox, "chatwoot-inbox", 0, "Chatwoot inbox id")
	fs.BoolVar(&f.skipReachCheck, "skip-reach-check", false, "skip HEAD reachability check (test/dev)")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if f.slug == "" || f.megaHost == "" || f.megaInstance == "" || f.megaToken == "" ||
		f.cwURL == "" || f.cwToken == "" || f.cwAccount == 0 || f.cwInbox == 0 {
		return f, errors.New("all --slug, --megaapi-*, --chatwoot-* flags are required")
	}
	return f, nil
}

func cmdTenantAdd(ctx context.Context, log zerolog.Logger, args []string) error {
	f, err := parseTenantFlags(args)
	if err != nil {
		return err
	}
	key, dsn, err := loadKeyAndDSN()
	if err != nil {
		return err
	}
	if !f.skipReachCheck {
		if err := reachAll(ctx, f.megaHost, f.cwURL); err != nil {
			return err
		}
	}
	db, err := bridge.NewDB(ctx, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	bearer, hmacSecret, ti, err := buildTenantInsert(f, key)
	if err != nil {
		return err
	}
	id, err := db.InsertTenant(ctx, ti)
	if err != nil {
		return err
	}
	log.Info().Str("tenant_id", id.String()).Str("slug", f.slug).Msg("tenant created")
	fmt.Printf("Tenant created: %s\nWebhook Bearer: %s\nHMAC Secret: %s\n", id, bearer, hmacSecret)
	return nil
}

func loadKeyAndDSN() ([]byte, string, error) {
	key, err := loadMasterKey()
	if err != nil {
		return nil, "", err
	}
	dsn, err := loadDSN()
	if err != nil {
		return nil, "", err
	}
	return key, dsn, nil
}

func reachAll(ctx context.Context, mega, cw string) error {
	if err := reachCheck(ctx, mega); err != nil {
		return fmt.Errorf("megaapi-host unreachable: %w", err)
	}
	if err := reachCheck(ctx, cw); err != nil {
		return fmt.Errorf("chatwoot-url unreachable: %w", err)
	}
	return nil
}

func buildTenantInsert(f tenantFlags, key []byte) (string, string, bridge.TenantInsert, error) {
	bearer := base64.RawURLEncoding.EncodeToString(bridge.RandomBytes(32))
	hmacSecret := base64.RawURLEncoding.EncodeToString(bridge.RandomBytes(32))
	enc := func(s string) ([]byte, error) { return bridge.Encrypt([]byte(s), key) }
	encMega, err := enc(f.megaToken)
	if err != nil {
		return "", "", bridge.TenantInsert{}, err
	}
	encCW, err := enc(f.cwToken)
	if err != nil {
		return "", "", bridge.TenantInsert{}, err
	}
	encBearer, err := enc(bearer)
	if err != nil {
		return "", "", bridge.TenantInsert{}, err
	}
	encHMAC, err := enc(hmacSecret)
	if err != nil {
		return "", "", bridge.TenantInsert{}, err
	}
	ti := bridge.TenantInsert{
		Slug:              f.slug,
		MegaAPIHost:       f.megaHost,
		MegaAPIInstance:   f.megaInstance,
		MegaAPITokenEnc:   encMega,
		ChatwootURL:       f.cwURL,
		ChatwootTokenEnc:  encCW,
		ChatwootAccountID: f.cwAccount,
		ChatwootInboxID:   f.cwInbox,
		HMACSecretEnc:     encHMAC,
		WebhookBearerEnc:  encBearer,
	}
	return bearer, hmacSecret, ti, nil
}

func reachCheck(ctx context.Context, rawURL string) error {
	cl := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
