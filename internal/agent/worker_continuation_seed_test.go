package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"supercli/internal/llm"
)

// Optional evaluation-only seed: replay the recorded initial assistant actions
// locally, then let the actual backend handle the new requirement. This avoids
// confounding the window comparison with a different initial model solution.
type continuationSeed struct {
	History []llm.Message
	Replies []llm.Message
	SHA256  string
}

func loadContinuationSeed(path, model string) (*continuationSeed, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var saved struct {
		Model  string
		Stages []struct {
			Correct  bool
			Messages []llm.Message
		}
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		return nil, err
	}
	if saved.Model != model || len(saved.Stages) == 0 || !saved.Stages[0].Correct {
		return nil, fmt.Errorf("seed must contain a verified initial stage for %s", model)
	}
	seed := &continuationSeed{History: saved.Stages[0].Messages}
	for _, m := range seed.History {
		if err := m.Validate(); err != nil {
			return nil, err
		}
		if m.Role == llm.RoleTool && len(m.Content) >= len(pruneMarkerPrefix) && m.Content[:len(pruneMarkerPrefix)] == pruneMarkerPrefix {
			return nil, fmt.Errorf("seed contains pruned results")
		}
		if m.Role == llm.RoleAssistant {
			seed.Replies = append(seed.Replies, m)
		}
	}
	if len(seed.Replies) == 0 || len(seed.Replies[len(seed.Replies)-1].ToolCalls) != 0 {
		return nil, fmt.Errorf("seed has no final assistant report")
	}
	historyBytes, err := json.Marshal(seed.History)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(historyBytes)
	seed.SHA256 = hex.EncodeToString(sum[:])
	return seed, nil
}

func replayContinuationReply(ctx context.Context, m llm.Message) (<-chan llm.Delta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deltas := []llm.Delta{{Role: llm.RoleAssistant}}
	for _, part := range m.Parts {
		if part.Type == llm.PartTypeReasoning && part.Reasoning != nil {
			deltas = append(deltas, llm.Delta{NativeReasoning: part.Reasoning})
		}
	}
	if text := messageDraftText(m); text != "" {
		deltas = append(deltas, llm.Delta{Content: text})
	}
	for i := range m.ToolCalls {
		call := m.ToolCalls[i]
		deltas = append(deltas, llm.Delta{ToolCall: &call})
	}
	deltas = append(deltas, llm.Delta{FinishReason: "stop"})
	stream := make(chan llm.Delta, len(deltas))
	for _, delta := range deltas {
		stream <- delta
	}
	close(stream)
	return stream, nil
}
