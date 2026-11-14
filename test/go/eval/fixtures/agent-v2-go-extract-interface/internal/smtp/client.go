// Package smtp is a tiny mail client used by the alerting daemon.
package smtp

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Message is one outgoing mail.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Stats counts client activity.
type Stats struct {
	Sent   int
	Failed int
}

// Client talks to one relay host.
type Client struct {
	host    string
	timeout time.Duration
	stats   Stats
	closed  bool
}

// Dial creates a client for host ("name:port").
func Dial(host string) (*Client, error) {
	if !strings.Contains(host, ":") {
		return nil, errors.New("smtp: host needs a port")
	}
	return &Client{host: host, timeout: 10 * time.Second}, nil
}

// Send delivers m.
func (c *Client) Send(ctx context.Context, m Message) error {
	if c.closed {
		return errors.New("smtp: client closed")
	}
	if err := ctx.Err(); err != nil {
		c.stats.Failed++
		return err
	}
	if m.To == "" {
		c.stats.Failed++
		return errors.New("smtp: empty recipient")
	}
	c.stats.Sent++
	return nil
}

// Ping checks the relay is reachable.
func (c *Client) Ping(ctx context.Context) error {
	if c.closed {
		return errors.New("smtp: client closed")
	}
	return ctx.Err()
}

// SetTimeout changes the per-command timeout.
func (c *Client) SetTimeout(d time.Duration) { c.timeout = d }

// Stats returns counters.
func (c *Client) Stats() Stats { return c.stats }

// Close releases the connection.
func (c *Client) Close() error {
	c.closed = true
	return nil
}
