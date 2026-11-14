package service

import (
	"context"
	"fmt"
	"strings"

	"alerts/internal/smtp"
)

// Digest batches low-priority notes into one mail.
type Digest struct {
	client Sender
	to     string
	items  []string
}

// NewDigest builds a Digest.
func NewDigest(c Sender, to string) *Digest {
	return &Digest{client: c, to: to}
}

// Add queues one note.
func (d *Digest) Add(note string) { d.items = append(d.items, note) }

// Flush mails all queued notes (if any) and clears the queue on success.
func (d *Digest) Flush(ctx context.Context) error {
	if len(d.items) == 0 {
		return nil
	}
	body := strings.Join(d.items, "\n")
	err := d.client.Send(ctx, smtp.Message{To: d.to, Subject: fmt.Sprintf("Digest (%d)", len(d.items)), Body: body})
	if err == nil {
		d.items = nil
	}
	return err
}
