package agent

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
)

// Keep the former projection as a value-level oracle: the optimization may
// change allocation/ownership, but must not change any provider-visible value.
func legacyMediaProjection(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = msg
		if len(msg.Parts) == 0 {
			continue
		}
		out[i].Parts = make([]llm.ContentPart, 0, len(msg.Parts))
		for _, part := range msg.Parts {
			if part.Type != llm.PartTypeImage || part.Image == nil {
				out[i].Parts = append(out[i].Parts, part)
				continue
			}
			img := *part.Image
			if img.Active {
				out[i].Parts = append(out[i].Parts, llm.ContentPart{Type: llm.PartTypeImage, Image: &img})
			} else {
				out[i].Parts = append(out[i].Parts, llm.ContentPart{Type: llm.PartTypeText, Text: sessionImageMarker(img)})
			}
		}
	}
	return out
}

func TestMediaProjectionValues(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "plain"},
		{Role: llm.RoleAssistant, Parts: []llm.ContentPart{
			{Type: llm.PartTypeReasoning, Reasoning: &llm.ReasoningBlock{Format: llm.ReasoningChat, Model: "model", Scope: "scope", Data: json.RawMessage("{\"reasoning_content\":\"native\"}")}},
			{Type: llm.PartTypeText, Text: "answer"},
		}, ToolCalls: []llm.ToolCall{{ID: "call", Name: "read_image", Arguments: "{\"path\":\"image.png\"}"}}},
		{Role: llm.RoleTool, Name: "read_image", ToolCallID: "call", Content: "result"},
		{Role: llm.RoleUser, Parts: []llm.ContentPart{
			{Type: llm.PartTypeText, Text: "before"},
			{Type: llm.PartTypeImage}, // Leave invalid/nil parts for provider validation.
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{ID: "active", Name: "żółć.png", Path: "missing.png", Data: "pixels", MediaType: "image/png", Active: true}},
			{Type: llm.PartTypeText, Text: "middle"},
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{ID: "old", Name: "old.png", Path: "missing-old.png"}},
			{Type: llm.PartTypeImage, Image: &llm.ImageRef{}},
			{Type: llm.PartTypeText, Text: "after"},
		}},
	}
	// Check every prefix too, including the no-image fast path.
	for n := 0; n <= len(messages); n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			before, err := json.Marshal(messages[:n])
			if err != nil {
				t.Fatal(err)
			}
			l := &Loop{Messages: messages[:n]}
			got := l.mediaProviderView(l.Messages)
			want := legacyMediaProjection(l.Messages)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("projection changed: got %#v want %#v", got, want)
			}
			after, _ := json.Marshal(l.Messages)
			if string(before) != string(after) {
				t.Fatal("projection modified history")
			}
		})
	}
	if got := (&Loop{}).mediaProviderView(nil); len(got) != 0 {
		t.Fatal("empty history acquired messages")
	}
}

func TestMediaProjectionActiveSnapshot(t *testing.T) {
	img := &llm.ImageRef{ID: "active", Path: "missing.png", Data: "pixels", Active: true}
	l := &Loop{Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{
		{Type: llm.PartTypeText, Text: "caption"},
		{Type: llm.PartTypeImage, Image: img},
	}}}}
	request := l.mediaProviderView(l.Messages)
	if request[0].Parts[1].Image == img {
		t.Fatal("request shares mutable image ref with history")
	}
	l.deactivateActiveImages()
	if img.Active || img.Data != "" {
		t.Fatal("live image was not deactivated")
	}
	snapshot := request[0].Parts[1].Image
	if !snapshot.Active || snapshot.Data != "pixels" {
		t.Fatal("deactivating history changed accepted request")
	}
	// Editing transformed parts cannot edit canonical history.
	request[0].Parts[0].Text = "edited"
	if l.Messages[0].Parts[0].Text != "caption" {
		t.Fatal("transformed parts share history backing array")
	}
	next := l.mediaProviderView(l.Messages)
	if next[0].HasImage() || !strings.Contains(next[0].Parts[1].Text, "active") {
		t.Fatal("dormant image did not become a reload handle")
	}
	// Reactivation is reflected on the next call, without invalidation/cache.
	img.Active = true
	reloaded := l.mediaProviderView(l.Messages)
	if !reloaded[0].HasImage() || next[0].HasImage() {
		t.Fatal("reload corrupted a previous projection")
	}
}

var mediaProjectionSink []llm.Message
var mediaPreparationSink int

func BenchmarkMediaProjection(b *testing.B) {
	for _, size := range []int{80, 1000} {
		for _, mode := range []string{"content", "parts", "one_image", "many_images"} {
			b.Run(fmt.Sprintf("%d/%s", size, mode), func(b *testing.B) {
				messages := make([]llm.Message, size)
				for i := range messages {
					messages[i] = llm.Message{Role: llm.RoleAssistant, Content: "answer"}
					if mode != "content" {
						messages[i].Content = ""
						messages[i].Parts = []llm.ContentPart{{Type: llm.PartTypeText, Text: "answer"}}
					}
					if mode == "many_images" || (mode == "one_image" && i == size-1) {
						messages[i].Parts = append(messages[i].Parts, llm.ContentPart{Type: llm.PartTypeImage,
							Image: &llm.ImageRef{ID: "image", Active: i%2 == 0}})
					}
				}
				l := &Loop{}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					mediaProjectionSink = l.mediaProviderView(messages)
				}
			})
		}
	}
}

// Include token estimation, tool definitions and final message assembly rather
// than presenting the media helper's speedup as the whole request speedup.
func BenchmarkContextPreparationParts(b *testing.B) {
	for _, size := range []int{80, 1000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			l := contextPreparationFixture(b, true)
			l.Messages = l.Messages[:1]
			for i := 0; i < size; i++ {
				role := llm.RoleAssistant
				if i%2 == 0 {
					role = llm.RoleUser
				}
				l.Messages = append(l.Messages, llm.Message{Role: role, Parts: []llm.ContentPart{
					{Type: llm.PartTypeText, Text: strings.Repeat("Finding. ", 100)},
				}})
			}
			l.invalidateVisibleEstimate()
			l.EstimateNextRequestTokens()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l.EstimateNextRequestTokens()
				l.EstimateNextRequestTokens()
				mediaPreparationSink = estimateRequestTokens(l.providerMessages(), l.buildToolDefs())
			}
		})
	}
}
