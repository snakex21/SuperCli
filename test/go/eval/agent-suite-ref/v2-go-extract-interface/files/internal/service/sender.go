package service

import (
	"context"

	"alerts/internal/smtp"
)

// Sender is the part of the mail client the service needs.
type Sender interface {
	Send(ctx context.Context, m smtp.Message) error
	Ping(ctx context.Context) error
}

var _ Sender = (*smtp.Client)(nil)
