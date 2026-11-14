package service

import (
	"context"
	"errors"
	"testing"

	"alerts/internal/smtp"
)

// hiddenFake implements exactly the methods the service package uses.
type hiddenFake struct {
	sent    []smtp.Message
	pingErr error
	sendErr error
}

func (f *hiddenFake) Send(_ context.Context, m smtp.Message) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *hiddenFake) Ping(context.Context) error { return f.pingErr }

var _ Sender = (*hiddenFake)(nil)
var _ Sender = (*smtp.Client)(nil)

func TestHiddenAlerterAndDigestWithFake(t *testing.T) {
	ctx := context.Background()
	f := &hiddenFake{}
	a := NewAlerter(f, "ops@x", 2)
	if err := a.Alert(ctx, 1, "low"); err != nil {
		t.Fatal(err)
	}
	if err := a.Alert(ctx, 3, "disk full\nsecond line"); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || f.sent[0].Subject != "[L3] disk full" || f.sent[0].To != "ops@x" {
		t.Fatalf("sent = %+v", f.sent)
	}
	if !a.Healthy(ctx) {
		t.Fatal("healthy fake reported unhealthy")
	}
	f.pingErr = errors.New("down")
	if a.Healthy(ctx) {
		t.Fatal("ping error ignored")
	}
	dg := NewDigest(f, "ops@x")
	dg.Add("a")
	dg.Add("b")
	f.sendErr = errors.New("boom")
	if err := dg.Flush(ctx); err == nil {
		t.Fatal("flush error swallowed")
	}
	f.sendErr = nil
	if err := dg.Flush(ctx); err != nil || f.sent[len(f.sent)-1].Subject != "Digest (2)" {
		t.Fatalf("flush = %v, %+v", err, f.sent)
	}
}
