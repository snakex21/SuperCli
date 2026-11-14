package service

import (
	"context"
	"testing"

	"alerts/internal/smtp"
)

type fakeSender struct{ sent []smtp.Message }

func (f *fakeSender) Send(_ context.Context, m smtp.Message) error {
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeSender) Ping(context.Context) error { return nil }

func TestAlerterUsesSender(t *testing.T) {
	f := &fakeSender{}
	a := NewAlerter(f, "ops", 2)
	_ = a.Alert(context.Background(), 1, "ignored")
	_ = a.Alert(context.Background(), 2, "kept")
	if len(f.sent) != 1 || f.sent[0].Subject != "[L2] kept" {
		t.Fatalf("sent = %+v", f.sent)
	}
}
