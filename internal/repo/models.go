// Package repo holds data-access types and queries.
//
// NOTE: queries/*.sql at the repo root are authoritative for future sqlc generation,
// but the implementation here is hand-coded against pgx because the sqlc CLI is not
// available in the F1 build environment. The hand-coded queries mirror the SQL files
// 1:1; running sqlc later should produce equivalent code.
package repo

import (
	"time"

	"github.com/google/uuid"
)

type MsgDirection string

const (
	DirectionInbound  MsgDirection = "inbound"
	DirectionOutbound MsgDirection = "outbound"
)

type MsgStatus string

const (
	StatusQueued    MsgStatus = "queued"
	StatusSending   MsgStatus = "sending"
	StatusDelivered MsgStatus = "delivered"
	StatusFailed    MsgStatus = "failed"
	StatusDuplicate MsgStatus = "duplicate"
)

type Tenant struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type MegaapiConfig struct {
	TenantID         uuid.UUID
	Host             string
	InstanceKey      string
	BearerTokenEnc   []byte
	BearerTokenKID   int16
	WebhookBearerEnc []byte
	WebhookBearerKID int16
	RateLimitRPS     int32
}

type ChatwootConfig struct {
	TenantID        uuid.UUID
	BaseURL         string
	APITokenEnc     []byte
	APITokenKID     int16
	AccountID       int32
	InboxID         int32
	InboxIdentifier *string
	HMACSecretEnc   []byte
	HMACSecretKID   int16
}

type Contact struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	WAJID            string
	CWContactID      int64
	CWConversationID *int64
	DisplayName      *string
	LastSeenAt       *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Message struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Direction   MsgDirection
	ExternalID  string
	CWMessageID *int64
	ContactID   *uuid.UUID
	Status      MsgStatus
	Payload     []byte // raw JSONB
	Attempts    int16
	LastError   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeliveredAt *time.Time
}
