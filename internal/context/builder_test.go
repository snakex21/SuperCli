package context

import (
	"context"
	"errors"
	"testing"
)

type stubLoader struct {
	name    string
	pri     int
	body    string
	failErr error
}

func (s *stubLoader) Name() string  { return s.name }
func (s *stubLoader) Priority() int { return s.pri }
func (s *stubLoader) Load() (Source, error) {
	if s.failErr != nil {
		return Source{}, s.failErr
	}
	return Source{Name: s.name, Body: s.body, Priority: s.pri}, nil
}

func TestBuilder_AddDuplicate(t *testing.T) {
	b := NewBuilder()
	b.MustAdd(&stubLoader{name: "x", pri: 1})
	err := b.Add(&stubLoader{name: "x", pri: 2})
	if err == nil {
		t.Fatal("expected error on duplicate name")
	}
}

func TestBuilder_AddNil(t *testing.T) {
	b := NewBuilder()
	if err := b.Add(nil); err == nil {
		t.Fatal("expected error on nil loader")
	}
}

func TestBuilder_Build_Aggregates(t *testing.T) {
	b := NewBuilder()
	b.MustAdd(&stubLoader{name: "a", pri: 10, body: "aaa"})
	b.MustAdd(&stubLoader{name: "b", pri: 100, body: "bbb"})
	sources, errs := b.Build(context.Background())
	if len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
	if sources.Len() != 2 {
		t.Fatalf("Len = %d", sources.Len())
	}
	if sources.Names()[0] != "a" {
		t.Errorf("order: %v", sources.Names())
	}
}

func TestBuilder_Build_ContinuesOnError(t *testing.T) {
	b := NewBuilder()
	b.MustAdd(&stubLoader{name: "good", pri: 10, body: "ok"})
	b.MustAdd(&stubLoader{name: "bad", pri: 10, failErr: errors.New("boom")})
	b.MustAdd(&stubLoader{name: "also_good", pri: 10, body: "ok"})
	sources, errs := b.Build(context.Background())
	if len(errs) != 1 {
		t.Errorf("errs = %v", errs)
	}
	if sources.Len() != 2 {
		t.Errorf("Len = %d, want 2 (bad skipped)", sources.Len())
	}
}

func TestBuilder_Build_RespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := NewBuilder()
	b.MustAdd(&stubLoader{name: "x", pri: 1, body: "x"})
	_, errs := b.Build(ctx)
	if len(errs) == 0 {
		t.Fatal("expected context error")
	}
}

func TestBuilder_Names(t *testing.T) {
	b := NewBuilder()
	b.MustAdd(&stubLoader{name: "a"})
	b.MustAdd(&stubLoader{name: "b"})
	if got := b.Names(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("Names = %v", got)
	}
}
