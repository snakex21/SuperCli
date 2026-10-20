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

func TestCompactUserContextRefusesDroppingIrreducibleInstructions(t *testing.T) {
	p := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "Goal: continue. State: preserve constraints.", FinishReason: "stop"}}}}
	l := newCompactionFailureLoop(t, p, NewAutoSummarizer(nil), 4096, nil)
	before, _ := json.Marshal(l.Messages)
	if _, err := l.CompactNow(context.Background()); err == nil || !strings.Contains(err.Error(), "insufficient reduction") {
		t.Fatalf("accepted reduction obtained by dropping a long original request: %v", err)
	}
	after, _ := json.Marshal(l.Messages)
	if string(before) != string(after) {
		t.Fatal("failed reduction changed history")
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
