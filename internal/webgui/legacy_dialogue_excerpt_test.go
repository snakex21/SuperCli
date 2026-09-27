package webgui

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/storage/memory"
	"supercli/internal/storage/session"
)

func legacyExcerptFixture(t testing.TB) (*Engine, string) {
	t.Helper()
	eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	store, err := eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(eng.Home(), "echo-test", "legacy excerpt")
	if err != nil {
		t.Fatal(err)
	}
	rows := []session.Encoded{
		{Role: "user", Content: "needle ORIGINAL_TASK keep project decisions across sessions"},
		{Role: "assistant", Content: strings.Repeat("Earlier implementation context. ", 45)},
		{Role: "user", Content: "Please verify the existing change."},
		{Role: "assistant", PartsJSON: `[{"type":"text","text":"LATEST_RESULT — verified parser fix; all tests passed."}]`},
	}
	for i := 0; i < 24; i++ {
		rows = append(rows, session.Encoded{Role: "assistant", ToolCallsJSON: `[{"ID":"read","Name":"read_lines","Arguments":"{}"}]`}, session.Encoded{Role: "tool", ToolCallID: "read", Content: strings.Repeat("ignored tool log ", 2048)})
	}
	rows = append(rows, session.Encoded{Role: "assistant", Content: "<thinking>private scratch work</thinking>"}, session.Encoded{Role: "assistant", PartsJSON: "{"})
	for _, row := range rows {
		if err := store.AppendMessage(context.Background(), sess.ID, row); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = eng.webMemoryStores(eng.Home())
	return eng, sess.ID
}

func TestLegacyRecallKeepsDialogueBehindToolTail(t *testing.T) {
	eng, _ := legacyExcerptFixture(t)
	got := eng.relevantLegacySessions(context.Background(), eng.Home(), "needle", "current-session", 420)
	for _, want := range []string{"ORIGINAL_TASK", "LATEST_RESULT", "all tests passed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("legacy recall lost %q: %q", want, got)
		}
	}
	for _, hidden := range []string{"ignored tool log", "private scratch work"} {
		if strings.Contains(got, hidden) {
			t.Fatalf("irrelevant payload leaked into recall: %q", hidden)
		}
	}
	if !utf8.ValidString(got) || memory.EstimateTokens(got) > 440 {
		t.Fatalf("invalid/bloated context: bytes=%d", len(got))
	}
}

func TestSessionRecallKeepsBeginningAndLatestResult(t *testing.T) {
	for _, filler := range []string{"long middle ", "zażółć 中文 😀 "} {
		source := "ORIGINAL_TASK " + strings.Repeat(filler, 300) + " LATEST_RESULT tests passed"
		if preview := compactSessionRecallText(source); len(preview) > 720 || !strings.Contains(preview, " … ") {
			t.Fatalf("preview violates its byte bound or hides the omission: bytes=%d", len(preview))
		}
		got := renderSessionRecallTexts([]string{source}, 420)
		if !strings.Contains(got, "ORIGINAL_TASK") || !strings.Contains(got, "LATEST_RESULT tests passed") {
			t.Fatalf("recall discarded an endpoint: %q", got)
		}
		if !utf8.ValidString(got) || memory.EstimateTokens(got) > 440 {
			t.Fatalf("invalid/bloated context: bytes=%d", len(got))
		}
	}
}

func BenchmarkLegacyRecallToolTail(b *testing.B) {
	eng, _ := legacyExcerptFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got := eng.relevantLegacySessions(context.Background(), eng.Home(), "needle", "current-session", 420)
		if got == "" {
			b.Fatal("recall empty")
		}
	}
}

func TestLegacyRecallSavedExcerpt(t *testing.T) {
	path := os.Getenv("SUPERCLI_LEGACY_EXCERPTS")
	if path == "" {
		t.Skip("set SUPERCLI_LEGACY_EXCERPTS to the private saved excerpt fixture")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Session  string
		Messages []struct {
			Role, Content, Name string
			PartsJSON           string `json:"parts_json"`
			ToolCallID          string `json:"tool_call_id"`
			ToolCallsJSON       string `json:"tool_calls_json"`
		}
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("empty replay")
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Session, func(t *testing.T) {
			eng, err := NewEngine(echoConfig(), t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer eng.Close()
			store, err := eng.sessionStore()
			if err != nil {
				t.Fatal(err)
			}
			sess, err := store.Create(eng.Home(), "echo-test", "saved excerpt")
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range fixture.Messages {
				if err := store.AppendMessage(context.Background(), sess.ID, session.Encoded{Role: row.Role, Content: row.Content, Name: row.Name, PartsJSON: row.PartsJSON, ToolCallID: row.ToolCallID, ToolCallsJSON: row.ToolCallsJSON}); err != nil {
					t.Fatal(err)
				}
			}
			raw, _, err := store.ReadMessagesBefore(context.Background(), sess.ID, 0, 16)
			if err != nil {
				t.Fatal(err)
			}
			beforeRows, beforeBytes := 0, 0
			for _, row := range raw {
				beforeBytes += len(row.Content) + len(row.PartsJSON)
				if webCapsuleText(row) != "" {
					beforeRows++
				}
			}
			excerpt, err := store.ReadDialogueExcerpt(context.Background(), sess.ID, 8, func(row session.Encoded) bool { return webCapsuleText(row) != "" })
			if err != nil {
				t.Fatal(err)
			}
			afterBytes := 0
			for _, row := range excerpt {
				afterBytes += len(row.Content) + len(row.PartsJSON)
			}
			want := buildWebSessionCapsule(sess.ID, excerpt)
			if want == "" {
				t.Fatal("saved fixture contains no useful dialogue")
			}
			ending := []rune(compactMemoryText(want, 0))
			suffix := string(ending[max(0, len(ending)-40):])
			got := eng.relevantLegacySessions(context.Background(), eng.Home(), "wcześniej", "new-session", 420)
			t.Logf("usable dialogue rows: %d -> %d; retained text/parts bytes: %d -> %d", beforeRows, len(excerpt), beforeBytes, afterBytes)
			if !strings.Contains(got, suffix) {
				t.Fatal("latest saved dialogue omitted from recall")
			}
			if !utf8.ValidString(got) || memory.EstimateTokens(got) > 440 {
				t.Fatalf("invalid/bloated replay context: bytes=%d", len(got))
			}
		})
	}
}
