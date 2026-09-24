package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type memoryOutputPersistence struct {
	values       map[string]string
	saves, reads int
	saveErr      error
}

func (s *memoryOutputPersistence) SaveToolOutput(_ context.Context, h, text string) error {
	s.saves++
	if s.saveErr != nil {
		return s.saveErr
	}
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[h] = text
	return nil
}
func (s *memoryOutputPersistence) ReadToolOutput(_ context.Context, h string) (string, error) {
	s.reads++
	text, ok := s.values[h]
	if !ok {
		return "", errors.New("expired output")
	}
	return text, nil
}

func TestPersistentOutputLoadsOnceAndKeepsSmallResultsFree(t *testing.T) {
	backend := &memoryOutputPersistence{}
	ctx := WithOutputPersistence(context.Background(), backend)
	first := NewOutputStore()
	if got := first.ModelContentContext(ctx, "read_lines", Result{Text: "small"}); got != "small" || backend.saves != 0 || backend.reads != 0 {
		t.Fatal("small result incurred I/O")
	}
	text := strings.Repeat("before ", 2000) + "hidden diagnostic" + strings.Repeat(" after", 2000)
	preview := first.ModelContentContext(ctx, "ctx_execute", Result{Text: "short capture", RetainedText: text})
	h := onlyOutputHandle(t, first)
	if !strings.Contains(preview, "saved for later turns") || backend.values[h] != text || backend.saves != 1 {
		t.Fatal("did not save original evidence")
	}
	next := NewOutputStore()
	found, err := next.search(ctx, readOutputArgs{Handle: h, Query: "hidden diagnostic"})
	if err != nil || !strings.Contains(found, "hidden diagnostic") || backend.reads != 1 {
		t.Fatalf("restore: %s %v", found, err)
	}
	chunk, _, _, err := next.readContext(ctx, h, 0, 100)
	if err != nil || chunk != text[:100] || backend.reads != 1 || backend.saves != 1 {
		t.Fatal("cache hit repeated disk I/O or changed bytes")
	}
}

func TestFailedOutputSaveRemainsUsableAndTruthful(t *testing.T) {
	backend := &memoryOutputPersistence{saveErr: errors.New("disk full")}
	ctx := WithOutputPersistence(context.Background(), backend)
	first := NewOutputStore()
	preview := first.CompactContext(ctx, "read_many", strings.Repeat("evidence ", 2000))
	h := onlyOutputHandle(t, first)
	if strings.Contains(preview, "saved for later turns") || !strings.Contains(preview, "in memory only") {
		t.Fatal("preview promised persistence after a write failure")
	}
	if _, _, _, err := first.readContext(ctx, h, 0, 100); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := NewOutputStore().readContext(ctx, h, 0, 100); err == nil {
		t.Fatal("missing persisted output resolved")
	}
}
