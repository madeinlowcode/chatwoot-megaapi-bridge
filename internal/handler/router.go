package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Routes wires up all bridge-api endpoints.
type Routes struct {
	Health   *Health
	Megaapi  *MegaapiWebhook
	Chatwoot *ChatwootWebhook
}

// Build returns the configured chi router.
func (rt *Routes) Build() http.Handler {
	r := chi.NewRouter()
	r.Use(RequestIDMiddleware, RecoverMiddleware, LoggerMiddleware)

	r.Get("/healthz", rt.Health.Healthz)
	r.Get("/readyz", rt.Health.Readyz)

	r.Post("/v1/wa/{slug}", rt.Megaapi.ServeHTTP)
	r.Post("/v1/cw/{slug}", rt.Chatwoot.ServeHTTP)

	return r
}
