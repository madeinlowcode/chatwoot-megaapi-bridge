// Package tenant resolves a slug to a fully-decrypted runtime config, with caching.
package tenant

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/crypto"
	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/repo"
)

// Resolved is everything a request handler or worker needs about a tenant.
type Resolved struct {
	ID          uuid.UUID
	Slug        string
	DisplayName string

	MegaapiHost          string
	MegaapiInstanceKey   string
	MegaapiBearerToken   string // decrypted; never log
	MegaapiWebhookBearer string // decrypted; never log; used to auth /v1/wa/{slug}
	MegaapiRateLimitRPS  int32

	ChatwootBaseURL         string
	ChatwootAPIToken        string // decrypted
	ChatwootAccountID       int32
	ChatwootInboxID         int32
	ChatwootInboxIdentifier string
	ChatwootHMACSecret      string // decrypted
}

// Lookuper is a contract implemented by Cache.
type Lookuper interface {
	Lookup(ctx context.Context, slug string) (*Resolved, error)
	LookupByID(ctx context.Context, id uuid.UUID) (*Resolved, error)
	Invalidate(slug string)
}

// ErrNotFound when slug is unknown or tenant inactive.
var ErrNotFound = errors.New("tenant: not found")

type cacheEntry struct {
	resolved *Resolved
	expires  time.Time
}

// Cache is an in-memory TTL cache backed by repo + keystore.
type Cache struct {
	q   *repo.Queries
	ks  *crypto.Keystore
	ttl time.Duration

	mu      sync.RWMutex
	entries map[string]cacheEntry
	max     int
}

// New returns a Cache with the given TTL and max size.
func New(q *repo.Queries, ks *crypto.Keystore, ttl time.Duration, max int) *Cache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if max <= 0 {
		max = 10_000
	}
	return &Cache{q: q, ks: ks, ttl: ttl, entries: make(map[string]cacheEntry), max: max}
}

// Lookup resolves a slug, hitting the in-memory cache first.
func (c *Cache) Lookup(ctx context.Context, slug string) (*Resolved, error) {
	c.mu.RLock()
	if e, ok := c.entries[slug]; ok && time.Now().Before(e.expires) {
		c.mu.RUnlock()
		return e.resolved, nil
	}
	c.mu.RUnlock()

	r, err := c.load(ctx, slug)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if len(c.entries) >= c.max {
		// simple eviction: drop one arbitrary expired or first iterated
		for k, v := range c.entries {
			if time.Now().After(v.expires) {
				delete(c.entries, k)
				break
			}
		}
		if len(c.entries) >= c.max {
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
	}
	c.entries[slug] = cacheEntry{resolved: r, expires: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return r, nil
}

// LookupByID resolves a tenant by its UUID. Falls back to a DB query that
// returns the slug, then proceeds via Lookup. This keeps cache keys uniform.
func (c *Cache) LookupByID(ctx context.Context, id uuid.UUID) (*Resolved, error) {
	// Scan in-memory cache first.
	c.mu.RLock()
	for _, e := range c.entries {
		if time.Now().Before(e.expires) && e.resolved.ID == id {
			r := e.resolved
			c.mu.RUnlock()
			return r, nil
		}
	}
	c.mu.RUnlock()

	// Fall back to DB: load slug, then full lookup.
	slug, err := c.slugByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return c.Lookup(ctx, slug)
}

func (c *Cache) slugByID(ctx context.Context, id uuid.UUID) (string, error) {
	const q = `SELECT slug FROM tenants WHERE id = $1`
	var slug string
	if err := c.q.Pool().QueryRow(ctx, q, id).Scan(&slug); err != nil {
		return "", err
	}
	return slug, nil
}

// Invalidate evicts a slug from the cache.
func (c *Cache) Invalidate(slug string) {
	c.mu.Lock()
	delete(c.entries, slug)
	c.mu.Unlock()
}

func (c *Cache) load(ctx context.Context, slug string) (*Resolved, error) {
	t, err := c.q.GetTenantBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	mc, err := c.q.GetMegaapiConfig(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	cw, err := c.q.GetChatwootConfig(ctx, t.ID)
	if err != nil {
		return nil, err
	}

	bearer, err := c.ks.DecryptToken(mc.BearerTokenEnc, mc.BearerTokenKID)
	if err != nil {
		return nil, err
	}
	webhookBearer, err := c.ks.DecryptToken(mc.WebhookBearerEnc, mc.WebhookBearerKID)
	if err != nil {
		return nil, err
	}
	apiToken, err := c.ks.DecryptToken(cw.APITokenEnc, cw.APITokenKID)
	if err != nil {
		return nil, err
	}
	hmacSecret, err := c.ks.DecryptToken(cw.HMACSecretEnc, cw.HMACSecretKID)
	if err != nil {
		return nil, err
	}

	r := &Resolved{
		ID:                   t.ID,
		Slug:                 t.Slug,
		DisplayName:          t.DisplayName,
		MegaapiHost:          mc.Host,
		MegaapiInstanceKey:   mc.InstanceKey,
		MegaapiBearerToken:   string(bearer),
		MegaapiWebhookBearer: string(webhookBearer),
		MegaapiRateLimitRPS:  mc.RateLimitRPS,
		ChatwootBaseURL:      cw.BaseURL,
		ChatwootAPIToken:     string(apiToken),
		ChatwootAccountID:    cw.AccountID,
		ChatwootInboxID:      cw.InboxID,
		ChatwootHMACSecret:   string(hmacSecret),
	}
	if cw.InboxIdentifier != nil {
		r.ChatwootInboxIdentifier = *cw.InboxIdentifier
	}
	return r, nil
}
