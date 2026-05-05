package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// Client wraps asynq.Client with typed helpers.
type Client struct {
	c *asynq.Client
}

// NewClient builds a queue client from a Redis connection option.
func NewClient(opt asynq.RedisConnOpt) *Client {
	return &Client{c: asynq.NewClient(opt)}
}

// Close releases asynq resources.
func (c *Client) Close() error { return c.c.Close() }

// Asynq returns the underlying asynq client (escape hatch for advanced cases).
func (c *Client) Asynq() *asynq.Client { return c.c }

// EnqueueWAtoCW puts an inbound job on the wa-to-cw queue.
func (c *Client) EnqueueWAtoCW(ctx context.Context, p WAtoCWPayload) error {
	return c.enqueue(ctx, TaskWAtoCW, QueueWAtoCW, p)
}

// EnqueueCWtoWA puts an outbound job on the cw-to-wa queue.
func (c *Client) EnqueueCWtoWA(ctx context.Context, p CWtoWAPayload) error {
	return c.enqueue(ctx, TaskCWtoWA, QueueCWtoWA, p)
}

func (c *Client) enqueue(ctx context.Context, taskType, queue string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("queue: marshal: %w", err)
	}
	t := asynq.NewTask(taskType, body)
	_, err = c.c.EnqueueContext(ctx, t,
		asynq.Queue(queue),
		asynq.MaxRetry(8),
		asynq.Retention(7*24*time.Hour),
	)
	return err
}
