// Package worker holds asynq job handlers.
package worker

// Retriable is implemented by errors classified as retry-eligible
// (5xx, timeouts, transient infra failures). Workers return these as-is so asynq
// can apply backoff and re-execute.
type Retriable interface {
	Retriable() bool
}

func isRetriable(err error) bool {
	if err == nil {
		return false
	}
	if r, ok := err.(Retriable); ok {
		return r.Retriable()
	}
	return false
}
