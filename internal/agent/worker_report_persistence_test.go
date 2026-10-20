package agent

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

func TestWorkerReportPersistenceRehydration(t *testing.T) {
	for _, mode := range []string{"task", "send_message", "background"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			store, err := session.OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			sess, err := store.Create(t.TempDir(), "echo", "encoded worker fixture")
			if err != nil {
				t.Fatal(err)
			}
			writer := session.NewWriter(store, sess.ID)
			provider, _ := llm.NewEcho("echo")
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), Writer: writer})
			if err != nil {
				t.Fatal(err)
			}
			encoded, original := encodedReportFixture(t, strings.Repeat("日本語 observation 🙂\n", 30)+"final", "\nEvidence suffix unchanged")
			// A preexisting raw row is not rewritten, even if it resembles a preview.
			legacy := llm.Message{Role: llm.RoleUser, Content: "old raw preview: " + workerReportCountedLabel}
			if err := writer.AppendMessage(ctx, legacy); err != nil {
				t.Fatal(err)
			}
			loop.Messages = []llm.Message{legacy}
			if mode == "background" {
				// Exercise the actual async-delivery callback and idle inbox drain.
				loop.backgroundDelivery(ctx)(ctx, encoded)
			} else {
				call := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "worker-call", Name: mode, Arguments: "{}"}}}
				loop.Messages = append(loop.Messages, call)
				if !loop.persist(ctx, call) {
					t.Fatal("assistant append not committed")
				}
				msg := llm.Message{Role: llm.RoleTool, Name: mode, ToolCallID: "worker-call", Content: encoded}
				loop.Messages = append(loop.Messages, msg)
				if !loop.persist(ctx, msg) {
					t.Fatal("worker append receipt changed")
				}
			}
			last := len(loop.Messages) - 1
			if loop.Messages[last].Content != encoded {
				t.Fatal("persist changed the active model representation")
			}
			checkRaw := func() {
				t.Helper()
				rows, err := store.ReadMessages(ctx, sess.ID)
				if err != nil || len(rows) != len(loop.Messages) {
					t.Fatalf("raw rows=%d err=%v", len(rows), err)
				}
				if rows[0].Content != legacy.Content || rows[last].Content != original {
					t.Fatal("raw transcript changed old history or omitted expanded report")
				}
				msg, err := rows[last].ToMessage()
				want := loop.Messages[last]
				want.Content = original
				// Raw SQL now preserves the host envelope independently of the
				// compact model projection, including the background producer.
				if err != nil || !reflect.DeepEqual(msg, want) {
					t.Fatalf("raw message metadata changed: err=%v", err)
				}
			}
			checkRaw()
			// Before a projection exists, resume must use full raw history.
			missing, err := store.ReadModelContext(ctx, sess.ID)
			if err != nil || len(missing) != len(loop.Messages) || missing[last].Content != original {
				t.Fatalf("missing projection fallback: %v", err)
			}
			wantProjection := append([]llm.Message(nil), loop.resolvedToolProviderView(loop.VisibleMessages())...)
			loop.persistCompletedProjection(ctx)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = session.OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			projected, err := store.ReadModelContext(ctx, sess.ID)
			if err != nil || !reflect.DeepEqual(projected, wantProjection) || projected[len(projected)-1].Content != encoded {
				t.Fatalf("durable model projection is not compact/exact: %v", err)
			}
			resumed, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), InitialMessages: projected})
			if err != nil {
				t.Fatal(err)
			}
			view := resumed.resolvedToolProviderView(resumed.VisibleMessages())
			if view[len(view)-1].Content != encoded {
				t.Fatal("rehydration expanded provider context")
			}
			checkRaw()
			// Damage only the fixture DB projection, never transcript rows.
			db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.ExecContext(ctx, "UPDATE session_context_projections SET messages_json=? WHERE session_id=?", "{", sess.ID); err != nil {
				t.Fatal(err)
			}
			fallback, err := store.ReadModelContext(ctx, sess.ID)
			if err != nil || !reflect.DeepEqual(fallback, missing) {
				t.Fatalf("corrupt projection did not fall back to complete raw history: %v", err)
			}
			checkRaw()
		})
	}
}

func TestWorkerReportPersistenceRunSavesCompactProjection(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	sess, err := store.Create(t.TempDir(), "echo", "automatic projection")
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := llm.NewEcho("echo")
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), Writer: session.NewWriter(store, sess.ID)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, original := encodedReportFixture(t, strings.Repeat("completed observation\n", 30), "\nEvidence")
	loop.backgroundDelivery(ctx)(ctx, encoded)
	// No direct projection save: Run must preserve the compact prefix itself.
	drainEvents(t, mustRun(t, loop, "Continue inspection."))
	projected, err := store.ReadModelContext(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	compact := 0
	for _, msg := range projected {
		if msg.Content == encoded {
			compact++
		}
	}
	rows, err := store.ReadMessages(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	full := 0
	for _, row := range rows {
		if row.Content == encoded {
			t.Fatal("compact model bytes leaked into raw history")
		}
		if row.Content == original {
			full++
		}
	}
	if compact != 1 || full != 1 {
		t.Fatalf("automatic end-of-Run projection: compact=%d full=%d", compact, full)
	}
}

func TestWorkerReportPersistenceRetryKeepsExpandedCopy(t *testing.T) {
	encoded, original := encodedReportFixture(t, strings.Repeat("observation\n", 20), "")
	writer := &flakyWriter{failAppends: 1}
	loop := &Loop{writer: writer}
	msg := llm.Message{Role: llm.RoleTool, Name: "task", ToolCallID: "id", Content: encoded}
	if loop.persist(context.Background(), msg) {
		t.Fatal("failed append incorrectly returned a receipt")
	}
	if len(loop.persistHealth.pending) != 1 || loop.persistHealth.pending[0].Message.Content != original {
		t.Fatal("retry buffer did not retain canonical report")
	}
	if !loop.persist(context.Background(), llm.Message{Role: llm.RoleUser, Content: "next"}) {
		t.Fatal("recovered append did not return receipt")
	}
	if len(writer.messages) != 2 || writer.messages[0].Content != original || writer.messages[1].Content != "next" || msg.Content != encoded {
		t.Fatal("retry reordered messages or mutated compact model copy")
	}
}

func TestWorkerReportPersistenceStaleSession(t *testing.T) {
	ctx := context.Background()
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old, err := store.Create(t.TempDir(), "echo", "old")
	if err != nil {
		t.Fatal(err)
	}
	next, err := store.Create(t.TempDir(), "echo", "next")
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := llm.NewEcho("echo")
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), Writer: session.NewWriter(store, old.ID)})
	if err != nil {
		t.Fatal(err)
	}
	deliver := loop.backgroundDelivery(ctx)
	current := []llm.Message{{Role: llm.RoleUser, Content: "next conversation"}}
	if err := loop.ResumeConversation(ctx, session.NewWriter(store, next.ID), current, nil); err != nil {
		t.Fatal(err)
	}
	before := append([]llm.Message(nil), loop.Messages...)
	encoded, original := encodedReportFixture(t, strings.Repeat("old worker observation\n", 20), "\nold evidence")
	deliver(ctx, encoded)
	if !reflect.DeepEqual(loop.Messages, before) {
		t.Fatal("stale report contaminated resumed conversation")
	}
	rows, err := store.ReadMessages(ctx, old.ID)
	if err != nil || len(rows) != 1 || rows[0].Content != original {
		t.Fatalf("stale direct append lost complete report: rows=%d err=%v", len(rows), err)
	}
	msg, err := rows[0].ToMessage()
	if err != nil || msg.Name != llm.WorkerNotificationName {
		t.Fatalf("stale append lost provenance: %v", err)
	}
	rows, err = store.ReadMessages(ctx, next.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("new session received stale append: rows=%d err=%v", len(rows), err)
	}
}
