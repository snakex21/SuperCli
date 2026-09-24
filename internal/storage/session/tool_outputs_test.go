package session

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestToolOutputSurvivesRestartAndFollowsHistoryReference(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.Create("project", "fixture", "source")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create("project", "fixture", "resumed")
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, source.ID)
	const h = "out_fixture"
	text := strings.Repeat("saved UTF-8 Żółć 😀", 2000)
	if err := writer.SaveToolOutput(ctx, h, text); err != nil {
		t.Fatal(err)
	}
	if err := writer.SaveToolOutput(ctx, h, "different bytes"); err == nil {
		t.Fatal("overwrote immutable handle")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	next := NewWriter(store, other.ID)
	if got, err := next.ReadToolOutput(ctx, h); err != nil || got != text {
		t.Fatalf("lost original: %v", err)
	}
	if _, err := next.ReadToolOutput(ctx, "out_000001"); err == nil {
		t.Fatal("legacy unknown handle resolved")
	}
	if _, err := store.db.ExecContext(ctx, "DELETE FROM sessions WHERE id=?", source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := next.ReadToolOutput(ctx, h); err == nil {
		t.Fatal("owner deletion retained output")
	}
}

func TestToolOutputCacheBoundsAndCancellation(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create("project", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, sess.ID)
	for i := 0; i < maxSavedOutputs+1; i++ {
		if err := writer.SaveToolOutput(ctx, fmt.Sprintf("out_%d", i), "small saved evidence"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.ReadToolOutput(ctx, "out_0"); err == nil {
		t.Fatal("oldest item survived cache limit")
	}
	if _, err := writer.ReadToolOutput(ctx, fmt.Sprintf("out_%d", maxSavedOutputs)); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", maxToolOutputBytes)
	for i := 0; i < 5; i++ {
		if err := writer.SaveToolOutput(ctx, fmt.Sprintf("out_big%d", i), big); err != nil {
			t.Fatal(err)
		}
	}
	var count, size int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(bytes),0) FROM tool_outputs").Scan(&count, &size); err != nil {
		t.Fatal(err)
	}
	if count != 4 || size != maxSavedOutputBytes {
		t.Fatalf("count=%d size=%d", count, size)
	}
	if _, err := writer.ReadToolOutput(ctx, "out_big0"); err == nil {
		t.Fatal("oldest large result survived byte limit")
	}
	if err := writer.SaveToolOutput(ctx, "out_oversized", big+"x"); err == nil {
		t.Fatal("oversized result accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := writer.SaveToolOutput(canceled, "out_canceled", "x"); err == nil {
		t.Fatal("canceled write succeeded")
	}
	if _, err := writer.ReadToolOutput(ctx, "out_canceled"); err == nil {
		t.Fatal("canceled write left a row")
	}
}

func BenchmarkSaveToolOutput(b *testing.B) {
	store, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create("project", "fixture", "")
	if err != nil {
		b.Fatal(err)
	}
	writer := NewWriter(store, sess.ID)
	text := strings.Repeat("output evidence ", 8192)
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := writer.SaveToolOutput(context.Background(), fmt.Sprintf("out_bench%d", i), text); err != nil {
			b.Fatal(err)
		}
	}
}

func TestConcurrentToolOutputsKeepDistinctEvidence(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create("project", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(store, sess.ID)
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func(i int) {
			results <- writer.SaveToolOutput(context.Background(), fmt.Sprintf("out_parallel%d", i), strings.Repeat(fmt.Sprintf("result%d ", i), 4000))
		}(i)
	}
	for i := 0; i < 16; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 16; i++ {
		got, err := writer.ReadToolOutput(context.Background(), fmt.Sprintf("out_parallel%d", i))
		if err != nil || got != strings.Repeat(fmt.Sprintf("result%d ", i), 4000) {
			t.Fatalf("result %d changed: %v", i, err)
		}
	}
}
