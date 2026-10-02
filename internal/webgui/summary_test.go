package webgui

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/llm"
)

type summaryProvider struct {
	text       string
	err        error
	onComplete func(context.Context, []llm.Message)
}

func (s summaryProvider) Name() string { return "summary-test" }

func (s summaryProvider) Complete(ctx context.Context, messages []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	if s.onComplete != nil {
		s.onComplete(ctx, messages)
	}
	if s.err != nil {
		return nil, s.err
	}
	out := make(chan llm.Delta, 3)
	go func() {
		defer close(out)
		select {
		case out <- llm.Delta{Role: llm.RoleAssistant, Content: s.text}:
		case <-ctx.Done():
			return
		}
		out <- llm.Delta{FinishReason: "stop"}
	}()
	return out, nil
}

func TestSummarizeHistoryMessage_FirstSentence(t *testing.T) {
	got := summarizeHistoryMessage("Napraw GUI. Potem odpal testy i build.", 90)
	if got != "Napraw GUI." {
		t.Fatalf("got %q", got)
	}
}

func TestSummarizeHistoryMessage_CollapsesWhitespace(t *testing.T) {
	got := summarizeHistoryMessage("  zrob\n\n  szybki   przeglad\tprojektu  ", 90)
	if got != "zrob szybki przeglad projektu" {
		t.Fatalf("got %q", got)
	}
}

func TestSummarizeHistoryMessage_StripsMarkdownAndCode(t *testing.T) {
	got := summarizeHistoryMessage("# Zadanie\n- sprawdz to:\n```go\nfmt.Println(1)\n```\ni napraw", 90)
	want := "Zadanie sprawdz to: [code] i napraw"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSummarizeHistoryMessage_TruncatesUnicode(t *testing.T) {
	got := summarizeHistoryMessage("zażółć gęślą jaźń bez kropki", 12)
	if got != "zażółć gęśl…" {
		t.Fatalf("got %q", got)
	}
}

func TestSummarizeHistoryMessage_Empty(t *testing.T) {
	if got := summarizeHistoryMessage(" \n\t ", 90); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestEngine_SummarizeHistoryMessageLLM_UsesProvider(t *testing.T) {
	eng := &Engine{prov: summaryProvider{text: `"Naprawa historii GUI"`}}
	got := eng.summarizeHistoryMessageLLM(context.Background(), "dlugi prompt", 90)
	if got != "Naprawa historii GUI" {
		t.Fatalf("got %q", got)
	}
}

func TestEngine_SummarizeHistoryMessageLLM_TruncatesProviderOutput(t *testing.T) {
	eng := &Engine{prov: summaryProvider{text: "bardzo dlugi tytul"}}
	got := eng.summarizeHistoryMessageLLM(context.Background(), "prompt", 8)
	if got != "bardzo…" {
		t.Fatalf("got %q", got)
	}
}

func TestCleanLLMSummary_StripsThinking(t *testing.T) {
	got := cleanLLMSummary("<thinking>We need a title</thinking> \"Naprawa sesji GUI\"")
	if got != "Naprawa sesji GUI" {
		t.Fatalf("got %q", got)
	}
}

func TestCleanLLMSummary_UnclosedThinkingBecomesEmpty(t *testing.T) {
	got := cleanLLMSummary("<thinking>We are asked to create a concise history title")
	if got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestSessionTopicRequestIsShortAndBounded(t *testing.T) {
	var captured []llm.Message
	prov := summaryProvider{text: "Naprawa przewijania GUI", onComplete: func(ctx context.Context, messages []llm.Message) { captured = append([]llm.Message(nil), messages...) }}
	source := "Napraw przewijanie GUI.\n\n" + strings.Repeat("Detailed ordinary context ", 3000)
	got := summarizeHistoryMessageWithProvider(context.Background(), source, 80, prov)
	if got != "Naprawa przewijania GUI" {
		t.Fatalf("title = %q", got)
	}
	if len(captured) != 2 {
		t.Fatalf("messages = %d", len(captured))
	}
	request := captured[1].Content
	if strings.Contains(request, "what was done") || strings.Contains(request, "first person") ||
		!strings.Contains(request, "topic or request") || !strings.Contains(request, "same language") {
		t.Fatalf("wrong title task: %q", request)
	}
	if runeLen(request) > 750 {
		t.Fatalf("unbounded title input: %d runes", runeLen(request))
	}
}
