package main

import (
	"strings"
	"testing"
	"time"
)

func TestStreamTimingChatAndResponses(t *testing.T) {
	start := time.Unix(0, 0)
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }

	chat := newStreamTiming(start)
	chat.observe([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"), at(100))
	chat.observe([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":42}}\n\ndata: [DONE]\n\n"), at(250))
	chat.finish(at(300))
	if chat.firstByte != 100*time.Millisecond || chat.firstDelta != 250*time.Millisecond || !strings.Contains(string(chat.usage), `"prompt_tokens":42`) {
		t.Fatalf("chat timing = %v %v %s", chat.firstByte, chat.firstDelta, chat.usage)
	}

	resp := newStreamTiming(start)
	resp.observe([]byte("event: response.created\ndata: {\"type\":\"response.created\"}\n\n"), at(80))
	resp.observe([]byte("data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"p\"}\n"), at(400))
	resp.observe([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":7,\"input_tokens_details\":{\"cached_tokens\":3}}}}"), at(500))
	resp.finish(at(500))
	if resp.firstByte != 80*time.Millisecond || resp.firstDelta != 400*time.Millisecond || !strings.Contains(string(resp.usage), `"input_tokens":7`) {
		t.Fatalf("responses timing = %v %v %s", resp.firstByte, resp.firstDelta, resp.usage)
	}
}
