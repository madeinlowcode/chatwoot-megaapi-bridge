package handler

import (
	"encoding/json"
	"net/http"

	"github.com/madeinlowcode/chatwoot-megaapi-bridge/internal/observability"
)

// ErrorCode values match docs/06.
const (
	CodeTenantNotFound   = "tenant_not_found"
	CodeAuthInvalid      = "auth_invalid"
	CodePayloadInvalid   = "payload_invalid"
	CodeDependencyDown   = "dependency_unavailable"
	CodeQueueFull        = "queue_full"
	CodeRateLimited      = "rate_limited"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeInternal         = "internal_error"
)

// writeError writes a JSON error envelope per docs/06.
func writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":      code,
		"message":    msg,
		"request_id": observability.RequestID(r.Context()),
	})
}
