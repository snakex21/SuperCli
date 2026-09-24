package llm

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestTextAssemblyCompatibility(t *testing.T) {
	atoms := []ContentPart{
		{Type: PartTypeText, Text: ""},
		{Type: PartTypeText, Text: "zażółć 🙂"},
		{Type: PartTypeText, Text: "\n"},
		{Type: PartTypeText, Text: "\x00\xff"},
		{Type: PartTypeReasoning, Reasoning: &ReasoningBlock{Format: ReasoningChat, Model: "pair-fixture", Scope: "fixture", Data: json.RawMessage("{\"reasoning_content\":\"native\"}")}},
		{Type: PartTypeImage, Image: &ImageRef{URL: "data:image/png;base64,AA=="}},
		{Type: PartTypeImage}, // Same provider error when vision is on.
		{Type: "unknown", Text: "not visible"},
	}
	check := func(parts []ContentPart) {
		t.Helper()
		m := Message{Role: RoleAssistant, Content: "ignored when Parts are present", Parts: parts}
		before, _ := json.Marshal(m)
		if got, want := messageText(m), legacyAssemblyMessageText(m); got != want {
			t.Fatalf("flat text got %q want %q, parts=%+v", got, want, parts)
		}
		for _, vision := range []bool{false, true} {
			got, err := encodeOpenAIContent(m, vision)
			want, wantErr := legacyAssemblyOpenAIContent(m, vision)
			if fmt.Sprint(err) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) {
				t.Fatalf("vision=%v got %#v (%v) want %#v (%v), parts=%+v", vision, got, err, want, wantErr, parts)
			}
		}
		after, _ := json.Marshal(m)
		if string(before) != string(after) {
			t.Fatal("assembly modified the input")
		}
	}
	check(nil)
	check([]ContentPart{})
	// All ordered triples cover leading/interleaved/trailing empty text,
	// ignored parts, and the image branch with errors in either order.
	for _, a := range atoms {
		check([]ContentPart{a})
		for _, b := range atoms {
			check([]ContentPart{a, b})
			for _, c := range atoms {
				check([]ContentPart{a, b, c})
			}
		}
	}
	rng := rand.New(rand.NewSource(7103))
	for n := 0; n < 1000; n++ {
		parts := make([]ContentPart, rng.Intn(32))
		for i := range parts {
			parts[i] = atoms[rng.Intn(len(atoms))]
		}
		check(parts)
	}
}

func textAssemblyHistory(mode string) []Message {
	messages := []Message{{Role: RoleSystem, Content: "Text assembly fixture"}}
	const text = "source line zażółć <tag>&value</tag>\n"
	for i := 0; i < 80; i++ {
		m := Message{Role: RoleUser, Content: strings.Repeat(text, 64)}
		if i%2 == 1 {
			m.Role = RoleAssistant
		}
		switch mode {
		case "parts":
			m.Parts = []ContentPart{{Type: PartTypeText, Text: m.Content}}
			m.Content = ""
		case "fragmented":
			for j := 0; j < 8; j++ {
				m.Parts = append(m.Parts, ContentPart{Type: PartTypeText, Text: strings.Repeat(text, 8)})
			}
			m.Content = ""
		}
		messages = append(messages, m)
	}
	return messages
}

func TestTextAssemblyRequestReplay(t *testing.T) {
	for _, mode := range []string{"content", "parts", "fragmented"} {
		messages := textAssemblyHistory(mode)
		for _, name := range []string{"chat", "responses", "anthropic"} {
			body, err := pairingRequestBuilders()[name](messages)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(body) {
				t.Fatal("invalid request")
			}
			t.Logf("wire %s/%s bytes=%d sha256=%x", name, mode, len(body), sha256.Sum256(body))
		}
	}
}

var assembledStringSink string
var assembledContentSink any
var assembledBodySink []byte

func BenchmarkTextAssembly(b *testing.B) {
	for _, name := range []string{"content", "short", "long", "reasoning", "fragmented"} {
		m := Message{Role: RoleAssistant}
		switch name {
		case "content":
			m.Content = strings.Repeat("x", 8192)
		case "short":
			m.Parts = []ContentPart{{Type: PartTypeText, Text: "krótka odpowiedź"}}
		case "long":
			m.Parts = []ContentPart{{Type: PartTypeText, Text: strings.Repeat("x", 8192)}}
		case "reasoning":
			m.Parts = []ContentPart{{Type: PartTypeReasoning}, {Type: PartTypeText, Text: strings.Repeat("x", 8192)}}
		case "fragmented":
			for i := 0; i < 64; i++ {
				m.Parts = append(m.Parts, ContentPart{Type: PartTypeText, Text: strings.Repeat("x", 128)})
			}
		}
		b.Run("flat/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				assembledStringSink = messageText(m)
			}
		})
		b.Run("chat/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				content, err := encodeOpenAIContent(m, false)
				if err != nil {
					b.Fatal(err)
				}
				assembledContentSink = content
			}
		})
	}
}

func BenchmarkTextAssemblyRequest(b *testing.B) {
	for _, name := range []string{"chat", "responses", "anthropic"} {
		for _, mode := range []string{"content", "parts", "fragmented"} {
			b.Run(name+"/"+mode, func(b *testing.B) {
				messages := textAssemblyHistory(mode)
				build := pairingRequestBuilders()[name]
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					body, err := build(messages)
					if err != nil {
						b.Fatal(err)
					}
					assembledBodySink = body
				}
			})
		}
	}
}

// Previous implementations retained as byte-level compatibility oracles.
func legacyAssemblyMessageText(m Message) string {
	if len(m.Parts) == 0 {
		return m.Content
	}
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Type == PartTypeText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func legacyAssemblyOpenAIContent(m Message, vision bool) (any, error) {
	// Most local OpenAI-compatible servers (LM Studio, Ollama,
	// llama.cpp) are happiest with plain string content for
	// text-only messages. Use multipart arrays only when an image
	// actually needs to be sent.
	if len(m.Parts) == 0 {
		return m.Content, nil
	}
	hasImage := false
	for _, p := range m.Parts {
		if p.Type == PartTypeImage && vision {
			hasImage = true
			break
		}
	}
	if !hasImage {
		var b strings.Builder
		for _, p := range m.Parts {
			switch p.Type {
			case PartTypeText:
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(p.Text)
			case PartTypeImage:
				if !vision {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(imageInputOmittedPlaceholder)
				}
			}
		}
		return b.String(), nil
	}
	return encodeOpenAIParts(m, vision)
}
