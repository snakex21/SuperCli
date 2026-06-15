package credits

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/storage"
)

func TestStatusLine_NoTracker(t *testing.T) {
	if got := StatusLine(nil, ""); got != "" {
		t.Errorf("StatusLine(nil) = %q, want empty", got)
	}
}

func TestStatusLine_NoCaps(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cs := NewStorage(db)
	if err := cs.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	tr := NewTracker("s1", Budget{}, cs)
	_ = tr.Record(context.Background(), 500, 200, "")
	got := StatusLine(tr, "")
	if !strings.HasPrefix(got, "credits:") {
		t.Errorf("missing credits prefix: %q", got)
	}
	if !strings.Contains(got, "700") { // 500+200
		t.Errorf("expected token count in status, got %q", got)
	}
	if !strings.Contains(got, "day ") {
		t.Errorf("expected day portion, got %q", got)
	}
}

func TestStatusLine_WithSessionCap(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cs := NewStorage(db)
	_ = cs.Migrate(context.Background())
	tr := NewTracker("s1", Budget{PerSession: 10_000}, cs)
	_ = tr.Record(context.Background(), 1000, 500, "")
	got := StatusLine(tr, "")
	if !strings.Contains(got, "1.5k/10k") {
		t.Errorf("expected session ratio, got %q", got)
	}
	if !strings.Contains(got, "15%") {
		t.Errorf("expected percentage, got %q", got)
	}
}

func TestStatusLine_WithDayCap(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cs := NewStorage(db)
	_ = cs.Migrate(context.Background())
	tr := NewTracker("s1", Budget{PerDay: 100_000}, cs)
	_ = tr.Record(context.Background(), 5000, 0, "")
	got := StatusLine(tr, "")
	if !strings.Contains(got, "day 5k/100k") {
		t.Errorf("expected day cap, got %q", got)
	}
}
