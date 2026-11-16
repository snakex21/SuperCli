package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func decodeCompactUserContext(t *testing.T, text string) compactUserContext {
	t.Helper()
	start := strings.LastIndex(text, compactUserContextStart)
	if start < 0 {
		t.Fatal("no retained user context")
	}
	body := text[start+len(compactUserContextStart):]
	end := strings.Index(body, compactUserContextEnd)
	if end < 0 {
		t.Fatal("incomplete retained user context")
	}
	var out compactUserContext
	if err := json.Unmarshal([]byte(body[:end]), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCompactUserContextKeepsUserSyntaxButExcludesTypedRuntime(t *testing.T) {
	request := "Keep this literal block:\n\n[settings]\nno publishing, no changed thinking level.\n[/settings]"
	m := llm.MessageWithContextBlocks(llm.RoleUser, []llm.ContextBlock{{Source: llm.SourceHuman, Text: request}, {Source: llm.SourceRuntime, Text: "VOLATILE-PREFLIGHT"}})
	if got := compactInstructionText(m); got != request {
		t.Fatal("literal user syntax was cut or runtime became an instruction")
	}
	m = llm.Message{Role: llm.RoleUser, Content: "legacy prefix", Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "attachment instruction", Source: llm.SourceHuman}, {Type: llm.PartTypeText, Text: "TOOL-RESULT", Source: llm.SourceToolResult}}}
	if got := compactInstructionText(m); got != "legacy prefix\nattachment instruction" {
		t.Fatal("multipart original request changed")
	}
}

// An oversized older request used to be carried unclipped, so compaction was
// refused ("insufficient reduction") and the session could wedge. It is now cut
// to a head+tail excerpt with an explicit marker inside the window budget; the
// newest request (the active turn) is never part of the summarized prefix.
func TestCompactUserContextTrimsOversizedRequestWithMarker(t *testing.T) {
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "Goal: continue. State: preserve constraints.", FinishReason: "stop"}}}}
	l := newCompactionFailureLoop(t, p, NewAutoSummarizer(nil), 4096, nil)
	if _, err := l.CompactNow(context.Background()); err != nil {
		t.Fatalf("oversized older request still blocks compaction: %v", err)
	}
	var summary string
	for _, m := range l.Messages {
		if IsLegacyCompactionSummary(m) {
			summary = m.Content
		}
	}
	carry := decodeCompactUserContext(t, summary)
	if len(carry.Requests) != 1 {
		t.Fatalf("carried requests = %d, want 1", len(carry.Requests))
	}
	got := carry.Requests[0]
	if !strings.HasPrefix(got, "Preserve every earlier requirement. ") || !strings.HasSuffix(got, "original request evidence ") ||
		!strings.Contains(got, "omitted to fit the compaction budget") {
		t.Fatalf("excerpt lost its head, tail or marker: %.200q", got)
	}
	if tok := compactCarryTokens(got); tok > compactUserContextBudget(4096) {
		t.Fatalf("excerpt %d tokens exceeds budget %d", tok, compactUserContextBudget(4096))
	}
	if last := l.Messages[len(l.Messages)-2]; last.Content != "Retain this previous correction." {
		t.Fatalf("active request was not kept verbatim: %q", last.Content)
	}
}

func TestCompactUserContextKeepsOldAndMiddleRestrictionsAcrossCompactions(t *testing.T) {
	legacy := WrapCompactSummary("State: keep all data portable; offline only. Pending: unfinished work.")
	legacyBody := compactTranscriptContent(llm.Message{Role: llm.RoleUser, Content: legacy})
	request := "Keep this entire request.\n" + strings.Repeat("Details 日本語 😀 ", 100) + "\nMIDDLE RESTRICTION: never change my reasoning level.\n" + strings.Repeat("More details ", 100) + "\nLast restriction: no publishing."
	msgs := []llm.Message{{Role: llm.RoleUser, Content: legacy}, {Role: llm.RoleUser, Content: request, Origin: &llm.ContextOrigin{Source: llm.SourceHuman}},
		{Role: llm.RoleUser, Content: "Worker report must not become a human instruction.", Origin: &llm.ContextOrigin{Source: llm.SourceWorkerNotification}}}
	for i := 0; i < 20; i++ {
		suffix := compactPreservedUserContext(msgs)
		carry := decodeCompactUserContext(t, suffix)
		if len(carry.Requests) != 1 || carry.Requests[0] != request || len(carry.PriorState) != 1 || carry.PriorState[0] != legacyBody {
			t.Fatal("user restrictions or legacy state were clipped or multiplied")
		}
		if strings.Count(suffix, compactUserContextStart) != 1 {
			t.Fatal("nested carries accumulated")
		}
		msgs = []llm.Message{{Role: llm.RoleUser, Content: WrapCompactSummary("Done: verified work." + suffix)}}
	}
}
