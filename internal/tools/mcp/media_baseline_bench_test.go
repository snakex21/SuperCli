package mcp

// Exact main8506d67 tools/call parser for text-only A/B benchmarks.
import (
	"encoding/json"
	"fmt"
)

func decodeToolResultBaseline(raw json.RawMessage) (Result, error) {
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Result{}, fmt.Errorf("mcp: tools/call decode: %w", err)
	}
	var text string
	for _, part := range parsed.Content {
		if part.Type == "text" {
			text += part.Text
		}
	}
	return Result{Text: text, IsError: parsed.IsError}, nil
}
