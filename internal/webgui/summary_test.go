package webgui

import (
	"context"
	"testing"

	"supercli/internal/llm"
)

type summaryProvider struct {
	text string
	err  error
}

func (s summaryProvider) Name() string { return "summary-test" }

func (s summaryProvider) Complete(ctx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
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
