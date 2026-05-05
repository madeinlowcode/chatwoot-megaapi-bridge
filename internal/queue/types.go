// Package queue defines asynq task types and an enqueue helper.
package queue

import "github.com/google/uuid"

// Task type names. asynq routes by these strings.
const (
	TaskWAtoCW = "wa-to-cw"
	TaskCWtoWA = "cw-to-wa"

	QueueWAtoCW = "wa-to-cw"
	QueueCWtoWA = "cw-to-wa"
)

// WAtoCWPayload is the inbound (megaAPI -> Chatwoot) job payload.
type WAtoCWPayload struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	MessageID  uuid.UUID `json:"message_id"`
	ExternalID string    `json:"external_id"`
}

// CWtoWAPayload is the outbound (Chatwoot -> megaAPI) job payload.
type CWtoWAPayload struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	MessageID  uuid.UUID `json:"message_id"`
	ExternalID string    `json:"external_id"`
}
