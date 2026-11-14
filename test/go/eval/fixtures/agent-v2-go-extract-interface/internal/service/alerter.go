// Package service turns events into mails.
package service

import (
	"context"
	"fmt"
	"strings"

	"alerts/internal/smtp"
)

// Alerter mails events at or above a minimum level.
type Alerter struct {
	client   *smtp.Client
	to       string
	minLevel int
}

// NewAlerter builds an Alerter.
func NewAlerter(c *smtp.Client, to string, minLevel int) *Alerter {
	return &Alerter{client: c, to: to, minLevel: minLevel}
}

// Alert mails msg when level >= minLevel. The subject is "[L<level>] "
// followed by the first line of msg.
func (a *Alerter) Alert(ctx context.Context, level int, msg string) error {
	if level < a.minLevel {
		return nil
	}
	first, _, _ := strings.Cut(msg, "\n")
	return a.client.Send(ctx, smtp.Message{To: a.to, Subject: fmt.Sprintf("[L%d] %s", level, first), Body: msg})
}

// Healthy reports whether the relay answers.
func (a *Alerter) Healthy(ctx context.Context) bool {
	return a.client.Ping(ctx) == nil
}
