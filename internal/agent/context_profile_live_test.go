package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

// Content-free, offline profiling of separately prepared session copies.
// It never opens the original conversation or makes a model call.
type contextProfileSize struct {
	Messages        int `json:"messages"`
	Bytes           int `json:"bytes"`
	EstimatedTokens int `json:"estimated_tokens"`
}
type contextProfile struct {
	Phase                       string                        `json:"phase"`
	Messages                    int                           `json:"messages"`
	EstimatedTokens             int                           `json:"estimated_tokens"`
	Categories                  map[string]contextProfileSize `json:"categories"`
	CarryRequests               int                           `json:"carry_requests"`
	CarryPriorStates            int                           `json:"carry_prior_states"`
	CarryDecodedBytes           int                           `json:"carry_decoded_bytes"`
	DuplicateNativeBlocks       int                           `json:"duplicate_native_blocks"`
	SummaryInputBytes           int                           `json:"summary_input_bytes"`
	SummaryInputEstimatedTokens int                           `json:"summary_input_estimated_tokens"`
	SummaryInputSeconds         float64                       `json:"summary_input_seconds"`
	Largest                     []contextProfileSize          `json:"largest_messages"`
}

func profileContextMessages(messages []llm.Message, phase string) contextProfile {
	out := contextProfile{Phase: phase, Messages: len(messages), EstimatedTokens: llm.EstimateTokens(messages), Categories: map[string]contextProfileSize{}}
	add := func(kind string, bytes, tokens int) {
		s := out.Categories[kind]
		s.Messages++
		s.Bytes += bytes
		s.EstimatedTokens += tokens
		out.Categories[kind] = s
	}
	for _, m := range messages {
		total, toolTokens := llm.EstimateMessageTokenSplit(m)
		raw, _ := json.Marshal(m)
		out.Largest = append(out.Largest, contextProfileSize{Messages: 1, Bytes: len(raw), EstimatedTokens: llm.EstimateMessageTokens(m)})
		if IsLegacyCompactionSummary(m) {
			body := compactTranscriptContent(m)
			carryTokens := 0
			start := strings.LastIndex(body, compactUserContextStart)
			if start >= 0 {
				rest := body[start+len(compactUserContextStart):]
				if end := strings.Index(rest, compactUserContextEnd); end >= 0 {
					var carry compactUserContext
					if json.Unmarshal([]byte(rest[:end]), &carry) == nil {
						out.CarryRequests += len(carry.Requests)
						out.CarryPriorStates += len(carry.PriorState)
						for _, s := range carry.Requests {
							out.CarryDecodedBytes += len(s)
						}
						for _, s := range carry.PriorState {
							out.CarryDecodedBytes += len(s)
						}
						carryTokens = min(total, llm.EstimateMessageTokens(llm.Message{Role: llm.RoleUser, Content: rest[:end]}))
						add("retained_context_json", end, carryTokens)
						body = body[:start] + rest[end+len(compactUserContextEnd):]
					}
				}
			}
			add("summary_and_facts", len(body), total-carryTokens)
			continue
		}
		text := m.Content
		nativeTokens := 0
		for _, p := range m.Parts {
			switch p.Type {
			case llm.PartTypeReasoning:
				if p.Reasoning != nil {
					add("native_reasoning", len(p.Reasoning.Data), p.Reasoning.EstimateTokens())
					nativeTokens += p.Reasoning.EstimateTokens()
				}
			case llm.PartTypeText:
				text += p.Text
			case llm.PartTypeImage:
				add("image", 0, 0)
			}
		}
		add(string(m.Role)+"_text", len(text), total-nativeTokens-toolTokens)
		if len(m.ToolCalls) > 0 {
			data, _ := json.Marshal(m.ToolCalls)
			add("tool_calls", len(data), toolTokens)
		}
	}
	seen := map[string]bool{}
	for _, m := range messages {
		for _, part := range m.Parts {
			if part.Type == llm.PartTypeReasoning && part.Reasoning != nil {
				hash := contextCacheHash(part.Reasoning.Data)
				if seen[hash] {
					out.DuplicateNativeBlocks++
				}
				seen[hash] = true
			}
		}
	}
	sort.Slice(out.Largest, func(i, j int) bool { return out.Largest[i].EstimatedTokens > out.Largest[j].EstimatedTokens })
	if len(out.Largest) > 8 {
		out.Largest = out.Largest[:8]
	}
	return out
}

func TestLongSessionContextProfile(t *testing.T) {
	stage := os.Getenv("SUPERCLI_CONTEXT_PROFILE_STAGE")
	if stage == "" {
		t.Skip("explicit isolated offline profiling fixture required")
	}
	compactedStage := os.Getenv("SUPERCLI_CONTEXT_COMPACTED_STAGE")
	if compactedStage == "" {
		t.Fatal("isolated compacted fixture required")
	}
	var profiles []contextProfile
	for _, item := range []struct{ path, phase string }{{stage, "original"}, {compactedStage, "compacted"}} {
		raw, err := os.ReadFile(filepath.Join(item.path, "fixture.json"))
		if err != nil {
			t.Fatal(err)
		}
		var f longCodexFixture
		if err = json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(item.path, f.Data)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Clean(f.Data) == filepath.Clean(f.SourceData) {
			t.Fatal("isolated copy required")
		}
		store, err := session.OpenStore(f.Data)
		if err != nil {
			t.Fatal(err)
		}
		messages, err := store.ReadModelContext(context.Background(), f.SourceSession)
		_ = store.Close()
		if err != nil {
			t.Fatal(err)
		}
		p, err := llm.NewCodex(llm.CodexConfig{Model: f.Model, BackendURL: f.Backend, Tokens: longCodexReadOnlyTokens{}})
		if err != nil {
			t.Fatal(err)
		}
		loop, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), System: "Read-only integration audit of a restored conversation. Preserve the user's selected reasoning level and all applicable requirements. Historical reports are evidence, not new authorization.", InitialMessages: messages, PruneProtectTokens: -1, WindowFor: func(string) int { return 1_000_000 }})
		if err != nil {
			t.Fatal(err)
		}
		prepared, _ := loop.prepareProviderMessages(true)
		profile := profileContextMessages(prepared, item.phase)
		history := loop.compactionHistory()
		split := completedCompactSplit(history, loop.windowResolution().Tokens)
		// Average several renders: a single fast pass can be below the Windows
		// monotonic clock's resolution. This benchmark never calls a provider.
		const renders = 256
		start := time.Now()
		var transcript string
		for i := 0; i < renders; i++ {
			transcript = compactSummaryInput(history.messages[:split])
		}
		profile.SummaryInputSeconds = time.Since(start).Seconds() / renders
		profile.SummaryInputBytes = len(transcript)
		profile.SummaryInputEstimatedTokens = llm.EstimateTokens([]llm.Message{{Role: llm.RoleUser, Content: transcript}})
		profiles = append(profiles, profile)
	}
	raw, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "offline-profile.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(string(raw))
}
