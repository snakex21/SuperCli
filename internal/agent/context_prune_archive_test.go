package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

type pruneArchiveProbe struct {
	saves int
	text  string
	fail  bool
}

func (p *pruneArchiveProbe) SaveToolOutput(_ context.Context, _ string, text string) error {
	p.saves++
	p.text = text
	if p.fail {
		return fmt.Errorf("fixture persistence unavailable")
	}
	return nil
}
func (p *pruneArchiveProbe) ReadToolOutput(context.Context, string) (string, error) {
	return "", fmt.Errorf("unexpected persistence read")
}

func archiveTestLoop(t *testing.T, p tools.OutputPersistence) *Loop {
	t.Helper()
	l, err := NewLoop(LoopConfig{Provider: echoProvider("archive"), Registry: tools.NewRegistry(), ToolOutputs: p})
	if err != nil {
		t.Fatal(err)
	}
	l.windowFor = func(string) int { return 1000 }
	l.pruneProtect = 1
	return l
}
func appendArchiveFixture(l *Loop, old, current string) {
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleTool, Name: "read_lines", Content: old}, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "current", Name: "read_lines", Arguments: "{}"}}}, llm.Message{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "current", Content: current})
}

func TestPruneArchiveOnlyWritesAfterGainGate(t *testing.T) {
	for _, kind := range []string{"below trigger", "disabled", "protected", "reference cost"} {
		t.Run(kind, func(t *testing.T) {
			p := &pruneArchiveProbe{}
			l := archiveTestLoop(t, p)
			old, current := strings.Repeat("x", 6000), strings.Repeat("y", 100)
			switch kind {
			case "below trigger":
				l.windowFor = func(string) int { return 1_000_000 }
			case "disabled":
				l.pruneProtect = -1
			case "protected":
				l.pruneProtect = 100000
			case "reference cost":
				old, current = strings.Repeat("x", 600), strings.Repeat("y", 1400)
			}
			appendArchiveFixture(l, old, current)
			if kind == "reference cost" {
				before := llm.EstimateMessageTokens(l.Messages[0])
				_, marker, ok := l.pruneCandidate(0, before)
				if !ok || before-marker < int(pruneMinGainFrac*float64(l.EstimateVisibleTokens())) {
					t.Fatal("fixture must otherwise allow pruning")
				}
			}
			if got := l.maybePruneToolResults(context.Background(), nil); got != 0 || p.saves != 0 || l.Messages[0].Content != old {
				t.Fatalf("unexpected mutation: reclaimed=%d saves=%d", got, p.saves)
			}
		})
	}
}

func TestPruneArchiveBoundsAndMemoryFallback(t *testing.T) {
	p := &pruneArchiveProbe{fail: true}
	l := archiveTestLoop(t, p)
	l.Messages = []llm.Message{{Role: llm.RoleUser, Content: "PRIVATE_USER"}, {Role: llm.RoleAssistant, Content: "PRIVATE_REASONING"}, {Role: llm.RoleTool, Name: "read_lines", Content: strings.Repeat("PRIVATE_HIDDEN", 100)}, {Role: llm.RoleTool, Name: "apply_skill", Content: strings.Repeat("PRIVATE_SKILL", 100)}}
	l.hidden = []bool{false, false, true, false}
	for i := 0; i < 700; i++ {
		l.Messages = append(l.Messages, llm.Message{Role: llm.RoleTool, Name: "read_lines", Content: fmt.Sprintf("RESULT_%d\n", i) + strings.Repeat("original evidence\n", 400)})
	}
	failureIndex := len(l.Messages)
	failure := "error: command_failed exit=7 (0.1s)\nstdout:\n" + strings.Repeat("failed diagnostic\n", 80)
	l.Messages = append(l.Messages, llm.Message{Role: llm.RoleTool, Name: "ctx_execute", Content: failure})
	shortIndex := len(l.Messages)
	short := strings.Repeat("s", 220)
	appendArchiveFixture(l, short, strings.Repeat("CURRENT_PROTECTED", 100))
	before := l.EstimateVisibleTokens()
	got := l.maybePruneToolResults(context.Background(), nil)
	if got <= 0 || got != before-l.EstimateVisibleTokens() {
		t.Fatal("wrong token accounting")
	}
	if p.saves != 1 || len(p.text) > pruneArchiveMaxBytes || len(p.text) < pruneArchiveMaxBytes-8192 {
		t.Fatalf("saves=%d archive bytes=%d", p.saves, len(p.text))
	}
	for _, private := range []string{"PRIVATE_USER", "PRIVATE_REASONING", "PRIVATE_HIDDEN", "PRIVATE_SKILL", "CURRENT_PROTECTED"} {
		if strings.Contains(p.text, private) {
			t.Fatalf("archived protected data %s", private)
		}
	}
	if l.Messages[shortIndex].Content != short {
		t.Fatal("short result should stay inline")
	}
	if !strings.HasSuffix(l.Messages[4].Content, "details omitted]") {
		t.Fatal("archive limit not enforced on oldest results")
	}
	marker := l.Messages[failureIndex].Content
	if !strings.Contains(marker, "exit_code=7") {
		t.Fatalf("failure lost: %s", marker)
	}
	_, raw, ok := strings.Cut(marker, "read_output ")
	if !ok {
		t.Fatal("failed persistence lost memory reference")
	}
	result := l.invoke(context.Background(), llm.ToolCall{ID: "recover", Name: "read_output", Arguments: strings.TrimSuffix(raw, "]")}, make(chan Event, 8))
	if result.failed || len(result.followUps) != 1 || !strings.Contains(result.followUps[0].Content, failure) {
		t.Fatalf("memory fallback: %+v", result)
	}
}

func TestPruneArchiveDoesNotDuplicateExistingOutputs(t *testing.T) {
	p := &pruneArchiveProbe{}
	l := archiveTestLoop(t, p)
	ctx := tools.WithOutputPersistence(context.Background(), p)
	existing := l.registry.ModelResultContentContext(ctx, "read_lines", tools.Result{Text: strings.Repeat("large result\n", 1500)})
	handle := handleInOutput(existing)
	if handle == "" || p.saves != 1 {
		t.Fatal("fixture was not retained")
	}
	appendArchiveFixture(l, existing, strings.Repeat("protected", 100))
	if l.maybePruneToolResults(ctx, nil) == 0 {
		t.Fatal("not pruned")
	}
	if p.saves != 1 || !strings.Contains(l.Messages[0].Content, handle) {
		t.Fatal("existing output was stored again")
	}
}

func BenchmarkInlineEvidencePruning(b *testing.B) {
	for _, persist := range []bool{false, true} {
		b.Run(fmt.Sprintf("persist=%t", persist), func(b *testing.B) {
			var output tools.OutputPersistence
			if persist {
				store, err := session.OpenStore(b.TempDir())
				if err != nil {
					b.Fatal(err)
				}
				defer store.Close()
				sess, err := store.Create("prune-bench", "fixture", "")
				if err != nil {
					b.Fatal(err)
				}
				output = session.NewWriter(store, sess.ID)
			}
			reg := tools.NewRegistry()
			reg.EnsureReadOutput()
			provider := echoProvider("prune-bench")
			var history []llm.Message
			for i := 0; i < 64; i++ {
				history = append(history, llm.Message{Role: llm.RoleTool, Name: "read_lines", Content: strings.Repeat("saved observation\n", 200)})
			}
			history = append(history, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "current", Name: "read_lines", Arguments: "{}"}}}, llm.Message{Role: llm.RoleTool, Name: "read_lines", Content: strings.Repeat("latest", 100)})
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				loop := &Loop{provider: provider, registry: reg, toolOutputs: output, modelID: "test", route: RouteCoordinator, windowFor: func(string) int { return 1000 }, pruneProtect: 1, Messages: append([]llm.Message(nil), history...)}
				if loop.maybePruneToolResults(context.Background(), nil) == 0 {
					b.Fatal("not pruned")
				}
			}
		})
	}
}
