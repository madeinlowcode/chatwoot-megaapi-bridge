package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/config"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/crypto"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/db"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/observability"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/repo"
)

func tenantsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tenants",
		Short: "Manage tenants",
	}
	cmd.AddCommand(tenantsCreateCmd(), tenantsListCmd(), tenantsShowCmd(), tenantsDeleteCmd())
	return cmd
}

type createFlags struct {
	slug, name string

	megaapiHost     string
	megaapiInstance string
	megaapiToken    string

	chatwootURL     string
	chatwootToken   string
	chatwootAccount int32
	chatwootInbox   int32
	chatwootInboxID string
	chatwootHMAC    string

	rateLimit int32

	publicWebhookBase string
}

func tenantsCreateCmd() *cobra.Command {
	var f createFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a tenant",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runCreateTenant(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.slug, "slug", "", "tenant slug (lowercase, [a-z0-9-])")
	cmd.Flags().StringVar(&f.name, "name", "", "display name")

	cmd.Flags().StringVar(&f.megaapiHost, "megaapi-host", "", "https://apibusinessN.megaapi.com.br")
	cmd.Flags().StringVar(&f.megaapiInstance, "megaapi-instance", "", "megaAPI instance key")
	cmd.Flags().StringVar(&f.megaapiToken, "megaapi-token", "", "megaAPI bearer token")

	cmd.Flags().StringVar(&f.chatwootURL, "chatwoot-url", "", "https://chatwoot.example.com")
	cmd.Flags().StringVar(&f.chatwootToken, "chatwoot-token", "", "chatwoot api_access_token")
	cmd.Flags().Int32Var(&f.chatwootAccount, "chatwoot-account", 0, "chatwoot account id")
	cmd.Flags().Int32Var(&f.chatwootInbox, "chatwoot-inbox", 0, "chatwoot inbox id (API channel)")
	cmd.Flags().StringVar(&f.chatwootInboxID, "chatwoot-inbox-identifier", "", "API channel identifier (optional)")
	cmd.Flags().StringVar(&f.chatwootHMAC, "chatwoot-hmac", "", "Chatwoot inbox HMAC secret (omit to generate)")

	cmd.Flags().Int32Var(&f.rateLimit, "rate-limit-rps", 20, "outbound rate limit per tenant (rps)")
	cmd.Flags().StringVar(&f.publicWebhookBase, "public-base-url", "https://your-bridge.example.com", "base URL printed for webhook config")

	_ = cmd.MarkFlagRequired("slug")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("megaapi-host")
	_ = cmd.MarkFlagRequired("megaapi-instance")
	_ = cmd.MarkFlagRequired("megaapi-token")
	_ = cmd.MarkFlagRequired("chatwoot-url")
	_ = cmd.MarkFlagRequired("chatwoot-token")
	_ = cmd.MarkFlagRequired("chatwoot-account")
	_ = cmd.MarkFlagRequired("chatwoot-inbox")
	return cmd
}

func runCreateTenant(ctx context.Context, f createFlags) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	observability.Init(cfg.LogLevel, "bridge-cli")

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	ks, err := crypto.LoadKeystoreFromEnv()
	if err != nil {
		return err
	}

	q := repo.New(pool)

	// Generate webhook bearer (used to authenticate megaAPI -> /v1/wa/{slug}).
	webhookBearer, err := randToken(32)
	if err != nil {
		return err
	}

	// Generate HMAC secret if not provided. Chatwoot configures HMAC at inbox level —
	// usually the operator pastes the value from Chatwoot UI; we accept generated
	// secret as a fallback for testing.
	hmacSecret := f.chatwootHMAC
	if hmacSecret == "" {
		hmacSecret, err = randToken(32)
		if err != nil {
			return err
		}
	}

	t, err := q.CreateTenant(ctx, f.slug, f.name)
	if err != nil {
		return fmt.Errorf("create tenant: %w", err)
	}

	bearerEnc, kid, err := ks.EncryptToken([]byte(f.megaapiToken))
	if err != nil {
		return err
	}
	whEnc, whKID, err := ks.EncryptToken([]byte(webhookBearer))
	if err != nil {
		return err
	}
	if err := q.UpsertMegaapiConfig(ctx, repo.MegaapiConfig{
		TenantID:         t.ID,
		Host:             f.megaapiHost,
		InstanceKey:      f.megaapiInstance,
		BearerTokenEnc:   bearerEnc,
		BearerTokenKID:   kid,
		WebhookBearerEnc: whEnc,
		WebhookBearerKID: whKID,
		RateLimitRPS:     f.rateLimit,
	}); err != nil {
		return fmt.Errorf("upsert megaapi config: %w", err)
	}

	apiEnc, apiKID, err := ks.EncryptToken([]byte(f.chatwootToken))
	if err != nil {
		return err
	}
	hmacEnc, hmacKID, err := ks.EncryptToken([]byte(hmacSecret))
	if err != nil {
		return err
	}
	var inboxIdent *string
	if f.chatwootInboxID != "" {
		v := f.chatwootInboxID
		inboxIdent = &v
	}
	if err := q.UpsertChatwootConfig(ctx, repo.ChatwootConfig{
		TenantID:        t.ID,
		BaseURL:         f.chatwootURL,
		APITokenEnc:     apiEnc,
		APITokenKID:     apiKID,
		AccountID:       f.chatwootAccount,
		InboxID:         f.chatwootInbox,
		InboxIdentifier: inboxIdent,
		HMACSecretEnc:   hmacEnc,
		HMACSecretKID:   hmacKID,
	}); err != nil {
		return fmt.Errorf("upsert chatwoot config: %w", err)
	}

	tid := t.ID
	_ = q.InsertAuditEvent(ctx, &tid, "tenant.created", true, nil)

	fmt.Println("✅ tenant created")
	fmt.Println()
	fmt.Println("Configure these webhooks in megaAPI and Chatwoot manually:")
	fmt.Println()
	fmt.Printf("  megaAPI webhook URL : %s/v1/wa/%s\n", f.publicWebhookBase, t.Slug)
	fmt.Printf("  megaAPI Bearer token: %s\n", webhookBearer)
	fmt.Println()
	fmt.Printf("  Chatwoot webhook URL: %s/v1/cw/%s\n", f.publicWebhookBase, t.Slug)
	if f.chatwootHMAC == "" {
		fmt.Printf("  Chatwoot HMAC secret: %s   (paste this into Chatwoot inbox settings)\n", hmacSecret)
	} else {
		fmt.Println("  Chatwoot HMAC secret: (provided by operator)")
	}
	return nil
}

func tenantsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tenants",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx := context.Background()
			cfg, err := config.FromEnv()
			if err != nil {
				return err
			}
			pool, err := db.NewPool(ctx, cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			tts, err := repo.New(pool).ListTenants(ctx)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SLUG\tNAME\tACTIVE\tCREATED")
			for _, t := range tts {
				fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", t.Slug, t.DisplayName, t.Active, t.CreatedAt.Format("2006-01-02"))
			}
			return tw.Flush()
		},
	}
}

func tenantsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <slug>",
		Short: "Show tenant details (no secrets)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := config.FromEnv()
			if err != nil {
				return err
			}
			pool, err := db.NewPool(ctx, cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			q := repo.New(pool)
			t, err := q.GetTenantBySlugAny(ctx, args[0])
			if err != nil {
				return err
			}
			mc, err := q.GetMegaapiConfig(ctx, t.ID)
			if err != nil {
				return err
			}
			cw, err := q.GetChatwootConfig(ctx, t.ID)
			if err != nil {
				return err
			}
			fmt.Printf("slug:           %s\n", t.Slug)
			fmt.Printf("name:           %s\n", t.DisplayName)
			fmt.Printf("active:         %t\n", t.Active)
			fmt.Printf("megaapi.host:   %s\n", mc.Host)
			fmt.Printf("megaapi.inst:   %s\n", mc.InstanceKey)
			fmt.Printf("chatwoot.url:   %s\n", cw.BaseURL)
			fmt.Printf("chatwoot.acct:  %d\n", cw.AccountID)
			fmt.Printf("chatwoot.inbox: %d\n", cw.InboxID)
			return nil
		},
	}
}

func tenantsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <slug>",
		Short: "Soft-delete (disable) a tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx := context.Background()
			cfg, err := config.FromEnv()
			if err != nil {
				return err
			}
			pool, err := db.NewPool(ctx, cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer pool.Close()
			if err := repo.New(pool).DisableTenant(ctx, args[0]); err != nil {
				return err
			}
			fmt.Printf("disabled %s\n", args[0])
			return nil
		},
	}
}

func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
