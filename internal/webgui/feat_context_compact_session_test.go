package webgui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
)

type compactSessionParentValueKey struct{}

// This observer uses the production transport's public header projection but
// sends no HTTP request. It checks the handler-to-provider context boundary,
// independently of OpenCode's existing wire serialization tests.
type compactSessionObserver struct {
	t             *testing.T
	wantSession   string
	wantDeadline  time.Time
	mode          string
	cancel        context.CancelFunc
	calls         int
	observedCalls []llm.CallStat
}

func (p *compactSessionObserver) Name() string { return "echo-test" }

func compactSessionHeader(t *testing.T, ctx context.Context) string {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://opencode.ai/zen/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	llm.ApplyOpenCodeZenHeaders(req, "https://opencode.ai/zen/v1")
	return req.Header.Get("X-OpenCode-Session")
}

func (p *compactSessionObserver) Complete(ctx context.Context, msgs []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
	p.calls++
	wantHeader := compactSessionHeader(p.t, llm.WithOpenCodeSession(context.Background(), p.wantSession))
	if got := compactSessionHeader(p.t, ctx); got != wantHeader {
		p.t.Errorf("manual compact session header=%q, want the normal conversation header %q", got, wantHeader)
	}
	if ctx.Value(compactSessionParentValueKey{}) != "parent-request" {
		p.t.Error("compaction dropped the parent request context value")
	}
	if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(p.wantDeadline) {
		p.t.Errorf("compaction deadline=%v/%v, want %v", deadline, ok, p.wantDeadline)
	}
	if got := llm.PurposeFromContext(ctx); got != llm.PurposeCompact {
		p.t.Errorf("purpose=%q, want compact", got)
	}
	if len(msgs) != 2 || msgs[0].Role != llm.RoleSystem || msgs[1].Role != llm.RoleUser || len(defs) != 0 {
		p.t.Errorf("compaction request shape changed: messages=%d tools=%d", len(msgs), len(defs))
	}
	if len(msgs) >= 2 && (!strings.Contains(msgs[1].Content, "old question") || strings.Contains(msgs[1].Content, "latest question")) {
		p.t.Error("compaction lost the old evidence or included the protected recent tail")
	}
	switch p.mode {
	case "failure":
		return nil, errors.New("deterministic compaction provider failure")
	case "cancel":
		p.cancel()
		if !errors.Is(ctx.Err(), context.Canceled) {
			p.t.Error("canceling the parent request did not cancel the helper context")
		}
		return nil, ctx.Err()
	}
	stream := make(chan llm.Delta, 1)
	stream <- llm.Delta{
		Content:      "Goal: finish the project.\nDone: inspected the old source.\nPending: continue with the latest question.",
		FinishReason: "stop",
		Usage:        &llm.Usage{Input: 120, Output: 24, CachedInput: 16, Reasoning: 5},
	}
	close(stream)
	return stream, nil
}

func TestContextCompactEndpointPropagatesKnownSession(t *testing.T) {
	for _, mode := range []string{"success", "failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			eng, err := NewEngine(echoConfig(), dir, dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = eng.Close() })
			store, err := eng.sessionStore()
			if err != nil {
				t.Fatal(err)
			}
			observer := &compactSessionObserver{t: t, mode: mode}
			eng.prov = llm.Metered(observer, config.ProviderOpenAI, llm.PurposeMain, func(stat llm.CallStat) {
				observer.observedCalls = append(observer.observedCalls, stat)
			})
			srv := NewServer(eng, false)
			var previousHeader string
			// One engine handling different saved sessions must not route both
			// helpers under a process-wide or previous-conversation fallback.
			for invocation := 0; invocation < 2; invocation++ {
				sess, err := store.Create(dir, "echo-test", "compact session context")
				if err != nil {
					t.Fatal(err)
				}
				writer := session.NewWriter(store, sess.ID)
				for _, msg := range []llm.Message{
					{Role: llm.RoleUser, Content: "old question " + strings.Repeat("x", 20000)},
					{Role: llm.RoleAssistant, Content: "old answer"},
					{Role: llm.RoleUser, Content: "latest question"},
					{Role: llm.RoleAssistant, Content: "latest answer"},
				} {
					if err := writer.AppendMessage(context.Background(), msg); err != nil {
						t.Fatal(err)
					}
				}
				archiveBefore, err := store.ReadMessages(context.Background(), sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				projectionBefore, err := store.ReadModelContext(context.Background(), sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				parent := context.WithValue(context.Background(), compactSessionParentValueKey{}, "parent-request")
				deadline := time.Now().Add(time.Minute)
				parent, cancel := context.WithDeadline(parent, deadline)
				observer.wantSession, observer.wantDeadline, observer.cancel = sess.ID, deadline, cancel
				req := httptest.NewRequest(http.MethodPost, "/api/context/compact", strings.NewReader(`{"session_id":"  `+sess.ID+`  "}`)).WithContext(parent)
				rec := httptest.NewRecorder()
				srv.handleContextCompact(rec, req)
				cancel()
				wantStatus := http.StatusOK
				if mode == "failure" {
					wantStatus = http.StatusBadGateway
				} else if mode == "cancel" {
					wantStatus = 499
				}
				if rec.Code != wantStatus {
					t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), wantStatus)
				}
				if observer.calls != invocation+1 || len(observer.observedCalls) != invocation+1 {
					t.Fatalf("provider calls=%d metered calls=%d, want %d", observer.calls, len(observer.observedCalls), invocation+1)
				}
				archiveAfter, err := store.ReadMessages(context.Background(), sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(archiveAfter, archiveBefore) {
					t.Fatal("manual compaction changed the durable transcript")
				}
				projectionAfter, err := store.ReadModelContext(context.Background(), sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				if mode != "success" {
					if !reflect.DeepEqual(projectionAfter, projectionBefore) {
						t.Fatal("failed or canceled compaction changed the durable model context")
					}
					continue
				}
				if len(projectionAfter) >= len(projectionBefore) || !agent.IsLegacyCompactionSummary(projectionAfter[0]) ||
					!reflect.DeepEqual(projectionAfter[len(projectionAfter)-2:], projectionBefore[len(projectionBefore)-2:]) {
					t.Fatal("successful compaction lost its summary or altered the protected recent tail")
				}
				usage, err := store.ReadUsage(context.Background(), sess.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(usage) != 1 || usage[0].Source != llm.PurposeCompact || usage[0].Input != 120 || usage[0].Output != 24 ||
					usage[0].CachedInput != 16 || usage[0].Reasoning != 5 {
					t.Fatalf("per-session usage sink lost or duplicated helper accounting: %+v", usage)
				}
				header := compactSessionHeader(t, llm.WithOpenCodeSession(context.Background(), sess.ID))
				if header == previousHeader {
					t.Fatal("different saved sessions share the same expected transport identity")
				}
				previousHeader = header
			}
		})
	}
}
