package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOutputStoreFailureSmallAndSelfContainedStayUnchanged(t *testing.T) {
	for _, result := range []Result{
		{Err: errors.New("unknown tool")},
		{Text: "small diagnostic", Err: errors.New("failed")},
		{Text: "already explained", Err: SelfContainedErr(errors.New("failed: already explained"))},
		{Text: strings.Repeat("detail", 1000), Err: SelfContainedErr(errors.New(strings.Repeat("detail", 1000)))},
	} {
		s := NewOutputStore()
		if got := s.ModelContent("ctx_execute", result); got != result.ModelContent() {
			t.Fatalf("complete/small failure changed: %s", got)
		}
		if len(s.entries) != 0 {
			t.Fatal("stored diagnostics already available inline")
		}
	}
}

func TestOutputStoreFailurePreservesTailAndRetrievableOriginal(t *testing.T) {
	text := strings.Repeat("start ", 1000) + "rare middle fact" + strings.Repeat(" end", 1000)
	result := Result{Text: text, Err: errors.New("tool failed")}
	s := NewOutputStore()
	content := s.ModelContent("custom_tool", result)
	if !strings.HasPrefix(content, result.ModelContent()) {
		t.Fatal("existing failure summary changed")
	}
	found, err := s.search(context.Background(), readOutputArgs{Handle: onlyOutputHandle(t, s), Query: "rare middle fact"})
	if err != nil || !strings.Contains(found, "rare middle fact") {
		t.Fatalf("diagnostics lost: %v %s", err, found)
	}
	// read_output failures never create handles for themselves.
	if got := s.ModelContent("read_output", result); got != result.ModelContent() {
		t.Fatal("recursive output retention")
	}
	if len(s.entries) != 1 {
		t.Fatalf("unexpected output handles: %d", len(s.entries))
	}
}

func TestOutputStoreFailureHonorsLimitsAndMissingStore(t *testing.T) {
	result := Result{Text: strings.Repeat("x", outputStoreBytes+1), Err: SelfContainedErr(errors.New("tool failed"))}
	s := NewOutputStore()
	content := s.ModelContent("ctx_execute", result)
	if !strings.HasPrefix(content, "error: tool failed") || !strings.Contains(content, "not retained") || strings.Contains(content, "handle=") {
		t.Fatalf("invalid oversized failure: %s", content)
	}
	if s.bytes != 0 || len(s.entries) != 0 {
		t.Fatal("retained output beyond memory limit")
	}
	var absent *OutputStore
	if got := absent.ModelContent("ctx_execute", result); got != result.ModelContent() {
		t.Fatal("missing store must preserve original failure summary")
	}
}
