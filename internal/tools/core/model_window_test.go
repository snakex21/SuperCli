package core

import (
	"context"
	"strings"
	"testing"
)

func TestModelWindowRoundTrip(t *testing.T) {
	if got := ModelWindow(context.Background()); got != 0 {
		t.Fatalf("empty context window = %d", got)
	}
	if got := ModelWindow(WithModelWindow(context.Background(), 32768)); got != 32768 {
		t.Fatalf("window = %d", got)
	}
	if got := ModelWindow(WithModelWindow(context.Background(), 0)); got != 0 {
		t.Fatalf("zero window recorded: %d", got)
	}
}

func TestWebFetchPageWindowStaysInline(t *testing.T) {
	store := NewOutputStore()
	page := strings.Repeat("ż", 9000) // 18 KB: above the generic limit, within web_fetch's
	if got := store.Compact("web_fetch", page); got != page {
		t.Fatal("default-sized web_fetch page was replaced by a preview")
	}
	if got := store.Compact("other_tool", page); got == page {
		t.Fatal("generic tool output was not compacted")
	}
	if got := store.Compact("web_fetch", strings.Repeat("a", ModelWebFetchInlineBytes+1)); !strings.HasPrefix(got, "[large tool output: ") {
		t.Fatal("oversized web_fetch output was not retained behind a preview")
	}
}

func TestReadBatchBudgetIsSharedByParallelCalls(t *testing.T) {
	ctx := WithModelWindow(context.Background(), 16384)
	one := ReadBatchBudget(ctx)
	if one != 16384*4*15/100 {
		t.Fatalf("budget=%d", one)
	}
	if got := ReadBatchBudget(WithReadBatchShare(ctx, 2)); got != one/2 {
		t.Fatalf("two calls: %d", got)
	}
	if got := ReadBatchBudget(WithReadBatchShare(ctx, 4)); got != readBatchShareMin {
		t.Fatalf("four calls must hit the floor: %d", got)
	}
	if got := ReadBatchBudget(WithModelWindow(context.Background(), 1_000_000)); got != readBatchMaxBudget {
		t.Fatalf("large window: %d", got)
	}
	if got := ReadBatchBudget(context.Background()); got != ModelReadBatchInlineBytes {
		t.Fatalf("unknown window: %d", got)
	}
}
