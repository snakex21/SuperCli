package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Offline investigation only. The history is a completed real receipt, but the
// provider callback deliberately stops before inference. No actual model speed
// or model quality is measured by these tests/benchmarks.
type firstCompactionProfileFixture struct {
	History       []llm.Message
	Prefix        []llm.Message
	System        string
	OriginalInput string
	Window        int
	SourceSHA     string
}

func loadFirstCompactionProfileFixture(tb testing.TB) firstCompactionProfileFixture {
	tb.Helper()
	path := os.Getenv("SUPERCLI_COMPACTION_FIRST_RECEIPT")
	if path == "" {
		tb.Skip("opt-in offline preparation experiment from a completed receipt")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	var receipt struct {
		Passed          bool             `json:"passed"`
		History         []llm.Message    `json:"history"`
		CompactEvent    AutoCompactEvent `json:"compact_event"`
		RequestMessages [][]llm.Message  `json:"request_messages"`
	}
	if err := json.Unmarshal(data, &receipt); err != nil || !receipt.Passed || len(receipt.RequestMessages) < 2 || len(receipt.RequestMessages[0]) != 2 || receipt.CompactEvent.Removed <= 0 || receipt.CompactEvent.Removed > len(receipt.History) {
		tb.Fatalf("completed actual compaction receipt required: %v", err)
	}
	fixture := firstCompactionProfileFixture{
		History: receipt.History, System: receipt.RequestMessages[1][0].Content,
		Window: receipt.CompactEvent.Window, SourceSHA: compactionQualitySHA(data),
		OriginalInput: receipt.RequestMessages[0][1].Content,
	}
	cleaned := (&Loop{}).cleanModelHistory(receipt.History)
	if len(cleaned) != len(receipt.History) {
		tb.Fatal("production projection changed archived message indices")
	}
	fixture.Prefix = cleaned[:receipt.CompactEvent.Removed]
	if receipt.RequestMessages[0][0].Content != compactionPrompt || RenderCompactTranscript(fixture.Prefix) != fixture.OriginalInput {
		tb.Fatal("production compactor input differs from recorded helper; no comparison allowed")
	}
	return fixture
}

// Only protocol JSON arguments are considered. Tool result text can be source
// code/JSON whose formatting itself matters; never rewrite it or ordinary user
// content. This trial is intentionally NOT used by the production renderer.
func firstCompactionCompactArgumentTrial(argument string) string {
	if len(argument) > 700 {
		return argument // preserve the exact existing excerpt boundaries
	}
	var compact bytes.Buffer
	compact.Grow(len(argument))
	if err := json.Compact(&compact, []byte(argument)); err != nil {
		return argument
	}
	if compact.Len() == len(argument) {
		return argument
	}
	return compact.String()
}

func firstCompactionArgumentRenderTrial(messages []llm.Message) string {
	// A test-only adaptation isolates input savings. Any real proposal would
	// integrate in argument rendering, not copy the complete history again.
	copyMessages := append([]llm.Message(nil), messages...)
	for i, message := range messages {
		if message.Role != llm.RoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		copyMessages[i].ToolCalls = append([]llm.ToolCall(nil), message.ToolCalls...)
		for j, call := range message.ToolCalls {
			copyMessages[i].ToolCalls[j].Arguments = firstCompactionCompactArgumentTrial(call.Arguments)
		}
	}
	return RenderCompactTranscript(copyMessages)
}

func TestFirstCompactionArgumentWhitespaceTrial(t *testing.T) {
	fixture := loadFirstCompactionProfileFixture(t)
	before, _ := json.Marshal(fixture.History)
	trial := firstCompactionArgumentRenderTrial(fixture.Prefix)
	after, _ := json.Marshal(fixture.History)
	if !bytes.Equal(before, after) {
		t.Fatal("offline trial modified canonical history")
	}
	var calls, changed, argumentBytes, saved int
	for _, message := range fixture.Prefix {
		if message.Role != llm.RoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			calls++
			argumentBytes += len(call.Arguments)
			result := firstCompactionCompactArgumentTrial(call.Arguments)
			if result != call.Arguments {
				changed++
				saved += len(call.Arguments) - len(result)
			}
		}
	}
	report := struct {
		SourceSHA, HistorySHA, InputSHA, TrialSHA string
		InputBytes, TrialBytes                    int
		Calls, Changed, ArgumentBytes, Saved      int
		CanonicalHistoryUnchanged                 bool
		Limitation                                string
	}{fixture.SourceSHA, compactionQualitySHA(before), compactionQualitySHA([]byte(fixture.OriginalInput)), compactionQualitySHA([]byte(trial)),
		len(fixture.OriginalInput), len(trial), calls, changed, argumentBytes, saved, true,
		"Offline syntactic experiment; no inference, prompt change, user configuration writes, or quality/speed claim. Test renderer adds copies and is not a production proposal."}
	if output := os.Getenv("SUPERCLI_COMPACTION_FIRST_OUT"); output != "" {
		parent, err := compactionQualityOutputParent(output)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(parent, 0700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(filepath.Join(parent, "argument-trial.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("offline argument whitespace trial: input %d -> %d bytes; changed %d/%d calls; arguments saved %d bytes", report.InputBytes, report.TrialBytes, changed, calls, saved)
}

func TestFirstCompactionArgumentTrialPreservesProtocolStrings(t *testing.T) {
	for _, argument := range []string{
		" { \"command\": \"node --test tests/invoice.test.cjs\\n  preserve  spaces\", \"path\": \"C:\\\\Próba 😀\\\\data\" } ",
		"{\n  \"search\": \"  if (x) {\\n    return y;\\n  }\",\n  \"replace\": \"\\treturn z;\\n\"\n}",
		` { "precise": 9007199254740993, "duplicate": 1, "duplicate": 2, "escaped": "\u0020\"" } `,
		"{invalid}", "", strings.Repeat(" ", 700) + "{}",
	} {
		result := firstCompactionCompactArgumentTrial(argument)
		if len(argument) > 700 || !json.Valid([]byte(argument)) {
			if result != argument {
				t.Fatal("invalid/previously truncated argument was changed")
			}
			continue
		}
		var want, got bytes.Buffer
		_ = json.Compact(&want, []byte(argument))
		_ = json.Compact(&got, []byte(result))
		if !bytes.Equal(want.Bytes(), got.Bytes()) {
			t.Fatal("argument protocol strings/numbers/key order/duplicates changed")
		}
	}
}

var errFirstCompactionPreparationProbe = errors.New("offline completion boundary; no inference")

func firstCompactionPreparationLoop(tb testing.TB, fixture firstCompactionProfileFixture) (*Loop, *int) {
	tb.Helper()
	calls := 0
	provider := newCompactionCompletionProvider(func(_ context.Context, messages []llm.Message, defs []llm.ToolDef) (<-chan llm.Delta, error) {
		calls++
		if len(messages) != 2 || messages[0].Content != compactionPrompt || messages[1].Content != fixture.OriginalInput || len(defs) != 0 {
			tb.Fatal("first compaction changed recorded input or offered tools")
		}
		return nil, errFirstCompactionPreparationProbe
	})
	l, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), Writer: &recordingWriter{}, System: fixture.System,
		InitialMessages: fixture.History, WindowFor: func(string) int { return fixture.Window }, Summarizer: NewAutoSummarizer(nil)})
	if err != nil {
		tb.Fatal(err)
	}
	l.route = RouteCoordinator
	return l, &calls
}

func TestFirstCompactionPreparationBoundary(t *testing.T) {
	fixture := loadFirstCompactionProfileFixture(t)
	l, calls := firstCompactionPreparationLoop(t, fixture)
	before, _ := json.Marshal(l.Messages)
	event, err := l.CompactNow(context.Background())
	after, _ := json.Marshal(l.Messages)
	if !errors.Is(err, errFirstCompactionPreparationProbe) || *calls != 1 || event.Removed != 0 || !bytes.Equal(before, after) {
		t.Fatalf("offline boundary changed history or did extra work: calls=%d event=%+v error=%v", *calls, event, err)
	}
	t.Logf("actual archived history=%d messages, loop projection=%d (leading systems=%d), one text-only callback, exact original input=%d bytes, history unchanged; no inference",
		len(fixture.History), len(l.Messages), leadingSystemCount(l.Messages), len(fixture.OriginalInput))
}

func BenchmarkFirstCompactionPreparation(b *testing.B) {
	fixture := loadFirstCompactionProfileFixture(b)
	b.Run("recorded-render", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			compactTranscriptPerfSink = RenderCompactTranscript(fixture.Prefix)
		}
	})
	b.Run("argument-trial-with-test-copies", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			compactTranscriptPerfSink = firstCompactionArgumentRenderTrial(fixture.Prefix)
		}
	})
	b.Run("post-helper-exact-facts", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			compactTranscriptPerfSink = CompactFacts(fixture.Prefix, nil)
		}
	})
	b.Run("CompactNow-to-Complete-without-main-tool-schemas", func(b *testing.B) {
		l, calls := firstCompactionPreparationLoop(b, fixture)
		b.ReportAllocs()
		for b.Loop() {
			if _, err := l.CompactNow(context.Background()); !errors.Is(err, errFirstCompactionPreparationProbe) {
				b.Fatalf("offline probe did not reach exactly one helper request: %v", err)
			}
		}
		if *calls != b.N {
			b.Fatalf("extra helper callbacks: %d calls for %d compaction attempts", *calls, b.N)
		}
	})
}
