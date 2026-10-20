package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Opt-in offline replay. Never export messages or call a configured backend.
// The custom summarizer retains ALL visible prefix text and tool arguments;
// it measures conservative packaging savings, not real summarizer quality/TTFT.
func TestLongSessionReadOnlyReplay(t *testing.T) {
	path, id := os.Getenv("SUPERCLI_LONG_SESSION_DB"), os.Getenv("SUPERCLI_LONG_SESSION_ID")
	if path == "" || id == "" {
		t.Skip("opt-in read-only local session replay; no inference")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(abs)
	if err != nil || !st.Mode().IsRegular() {
		t.Fatal("existing database required")
	}
	uriPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var guarded int
	if err = tx.QueryRowContext(ctx, "PRAGMA query_only").Scan(&guarded); err != nil || guarded != 1 {
		t.Fatal("read-only guard required")
	}
	var raw []byte
	var through, archiveCount int
	if err = tx.QueryRowContext(ctx, "SELECT through_seq,messages_json FROM session_context_projections WHERE session_id=?", id).Scan(&through, &raw); err != nil {
		t.Fatal(err)
	}
	var messages []llm.Message
	if err = json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM messages WHERE session_id=?", id).Scan(&archiveCount); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT role,IFNULL(content,''),IFNULL(parts_json,''),IFNULL(name,''),IFNULL(tool_call_id,''),IFNULL(tool_calls_json,'') FROM messages WHERE session_id=? AND seq>? ORDER BY seq`, id, through)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var m llm.Message
		var parts, calls string
		if err = rows.Scan(&m.Role, &m.Content, &parts, &m.Name, &m.ToolCallID, &calls); err != nil {
			t.Fatal(err)
		}
		if parts != "" {
			if err = json.Unmarshal([]byte(parts), &m.Parts); err != nil {
				t.Fatal(err)
			}
		}
		if calls != "" {
			if err = json.Unmarshal([]byte(calls), &m.ToolCalls); err != nil {
				t.Fatal(err)
			}
		}
		messages = append(messages, m)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(messages)
	originalHash := sha256.Sum256(original)
	started, release := make(chan struct{}), make(chan struct{})
	var captured []llm.Message
	l := newSpeculativeLoop(t, func(ctx context.Context, _ llm.Provider, prefix []llm.Message) (string, error) {
		captured = cloneCompactPrefix(prefix)
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		var b strings.Builder
		for _, m := range prefix {
			if m.Role == llm.RoleSystem {
				continue
			}
			fmt.Fprintf(&b, "[%s] %s\n", m.Role, m.TextOnly().Content)
			for _, call := range m.ToolCalls {
				fmt.Fprintf(&b, "[tool call %s %s] %s\n", call.ID, call.Name, call.Arguments)
			}
		}
		return WrapCompactSummary(b.String() + compactPreservedUserContext(prefix)), nil
	})
	defer l.stopSpeculativeCompact()
	l.LoadConversation(messages)
	l.route = RouteCoordinator
	before := l.estimateNextRequestTokensRaw()
	beforeMessages := len(l.Messages)
	beforeBody := cloneCompactPrefix(l.Messages)
	oldEffort, oldDiscard := llm.ReasoningEffort(), l.discardPreviousReasoning
	history := l.compactionHistory()
	split := history.originalSplit(history.autoCompactSplit())
	suffix := cloneCompactPrefix(l.Messages[split:])
	window := before * 5 / 4
	l.windowFor = func(string) int { return window }
	if threshold := autoCompactThreshold(window); before > threshold || float64(before) < speculativeCompactFrac*float64(threshold) {
		t.Fatal("long fixture is outside speculation band")
	}
	if err = l.maybeAutoCompact(ctx, nil, ""); err != nil {
		t.Fatal(err)
	}
	waitCompactTest(t, started)
	close(release)
	waitCompactTest(t, l.speculative.done)
	if err = l.maybeAutoCompact(ctx, nil, ""); err != nil {
		t.Fatal(err)
	}
	after := l.estimateNextRequestTokensRaw()
	if after >= before || len(l.Messages) != len(suffix)+1 {
		t.Fatalf("no replay reduction: before=%d after=%d", before, after)
	}
	for i := range suffix {
		a, _ := json.Marshal(suffix[i])
		b, _ := json.Marshal(l.Messages[i+1])
		if string(a) != string(b) {
			t.Fatal("active native state or recent instruction changed")
		}
	}
	for _, m := range captured {
		if m.Role == llm.RoleSystem {
			continue
		}
		if text := m.TextOnly().Content; text != "" && !strings.Contains(l.Messages[0].Content, text) {
			t.Fatal("visible prefix evidence was lost")
		}
		for _, call := range m.ToolCalls {
			if !strings.Contains(l.Messages[0].Content, call.Arguments) {
				t.Fatal("tool arguments lost")
			}
		}
	}
	unchanged, _ := json.Marshal(messages)
	if originalHash != sha256.Sum256(unchanged) || oldEffort != llm.ReasoningEffort() || oldDiscard != l.discardPreviousReasoning {
		t.Fatal("archive or reasoning policy changed")
	}
	bench := func(msgs []llm.Message) testing.BenchmarkResult {
		view, err := NewLoop(LoopConfig{Provider: &stubProvider{name: "fixture"}, Registry: tools.NewRegistry(), InitialMessages: msgs})
		if err != nil {
			t.Fatal(err)
		}
		view.route = RouteCoordinator
		return testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, _ = view.prepareProviderMessages(true)
			}
		})
	}
	oldCost, newCost := bench(beforeBody), bench(l.Messages)
	t.Logf("OFFLINE ONLY: archive_rows=%d resumed_messages=%d -> %d; estimated_request_tokens=%d -> %d; reduction=%.1f%%; all visible prefix text, tool arguments, original archive and active tail preserved; no model inference", archiveCount, beforeMessages, len(l.Messages), before, after, 100*(1-float64(after)/float64(before)))
	t.Logf("local preparation only: before=%.3f ms/op %d B/op after=%.3f ms/op %d B/op", float64(oldCost.NsPerOp())/1e6, oldCost.AllocedBytesPerOp(), float64(newCost.NsPerOp())/1e6, newCost.AllocedBytesPerOp())
}
