package llm

import "testing"

func TestAnthropicPromptCacheSpacesBreakpointsInsideLookback(t *testing.T) {
	var msgs []anthropicMessage
	for i := 0; i < 60; i++ {
		msgs = append(msgs, anthropicMessage{Role: "user", Content: []anthropicContentBlock{{Type: "text", Text: "block"}}})
	}
	req := anthropicRequest{System: "stable", Messages: msgs}
	if applyAnthropicPromptCache(&req, true) {
		t.Fatal("system fallback requested although history can carry markers")
	}
	var marked []int
	for i, m := range req.Messages {
		if m.Content[0].CacheControl != nil {
			marked = append(marked, i)
		}
	}
	if len(marked) != anthropicMaxMessageBreakpoints {
		t.Fatalf("markers at %v, want %d", marked, anthropicMaxMessageBreakpoints)
	}
	if marked[len(marked)-1] != len(msgs)-2 {
		t.Fatalf("newest marker %d must precede the refreshed tail", marked[len(marked)-1])
	}
	for i := 1; i < len(marked); i++ {
		if gap := marked[i] - marked[i-1]; gap >= 20 {
			t.Fatalf("gap %d escapes the 20-block lookback: %v", gap, marked)
		}
	}
}
