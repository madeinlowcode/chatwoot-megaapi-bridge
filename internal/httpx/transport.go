// Package httpx provides a shared HTTP transport tuned per docs/07.
package httpx

import (
	"net"
	"net/http"
	"sync"
	"time"
)

var (
	once   sync.Once
	shared *http.Client
)

// SharedClient returns a process-wide HTTP client tuned for outbound calls.
func SharedClient() *http.Client {
	once.Do(func() {
		tr := &http.Transport{
			MaxIdleConns:        500,
			MaxIdleConnsPerHost: 50,
			IdleConnTimeout:     90 * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
		}
		shared = &http.Client{
			Transport: tr,
			Timeout:   30 * time.Second,
		}
	})
	return shared
}

// SetClient overrides the shared client. Intended for tests only.
func SetClient(c *http.Client) {
	once.Do(func() {})
	shared = c
}
