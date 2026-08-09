// Package llm defines the provider interface for language models.
// All types here are provider-agnostic; concrete providers (OpenAI-compat,
// Anthropic, ...) live in their own files.
package llm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Role is the role of a message in a conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a single chat message. A message is either plain text
// (set Content, leave Parts empty) OR multimodal (set Parts, Content
// is ignored). The two are never mixed at the wire level: a provider
// that supports multimodal will see Parts; one that does not will
// fall back to Content and TextOnly().
//
// Name is set on tool messages and identifies which tool produced
// the result. It is also used for assistant tool calls (the function
// name).
//
// ToolCallID is set on RoleTool messages and matches the ID of a
// tool call on the preceding assistant message.
//
// ToolCalls is set on RoleAssistant messages when the model wants
// to invoke tools; the agent loop executes them and appends
// corresponding RoleTool result messages.
// providerSafeToolArguments guarantees that replayed assistant tool calls
// contain syntactically valid JSON. This is a last-resort transport guard for
// legacy/corrupt sessions; the agent normally repairs arguments before they are
// persisted. Empty or invalid historical arguments become an empty object so a
// provider rejects neither the whole conversation nor the user's next turn.
func providerSafeToolArguments(raw string) string {
	args := strings.TrimSpace(raw)
	if args == "" || !json.Valid([]byte(args)) {
		return "{}"
	}
	return args
}

type Message struct {
	Role    Role
	Content string
	Parts   []ContentPart
	Name    string

	// ToolCallID is set on RoleTool messages.
	ToolCallID string

	// ToolCalls is set on RoleAssistant messages that contain
	// tool invocations.
	ToolCalls []ToolCall
}

// PartType is the discriminator for ContentPart.Type.
type PartType string

const (
	PartTypeText  PartType = "text"
	PartTypeImage PartType = "image"
)

// ContentPart is one chunk of a multimodal message. Exactly one of
// Text or Image is meaningful, based on Type.
type ContentPart struct {
	Type  PartType
	Text  string
	Image *ImageRef
}

// ImageRef references an image by remote/data URL, inline base64, or a local
// file path. Path is preferred for durable tool-generated images: it keeps
// multi-megabyte base64 blobs out of conversation history and loads the bytes
// only when a vision-capable provider actually needs them.
type ImageRef struct {
	URL       string // http(s) URL or "data:<mediatype>;base64,<data>" URI
	MediaType string // "image/png" | "image/jpeg" | "image/webp" | "image/gif"
	Data      string // raw base64 (no prefix); legacy/transient inline representation
	Path      string // local image file; loaded lazily only for vision requests
	ID        string // stable session media id, e.g. img_a1b2c3d4e5f6
	Name      string // optional human-facing source name (auto.jpg, screenshot, ...)
	Active    bool   // send pixels on the next provider call only; never persist true
}

// AsDataURI returns a data: URI suitable for OpenAI's image_url.url
// field. If URL is already a data: URI it is returned as-is. Inline
// Data is composed with MediaType. File-backed refs intentionally are
// not loaded here; provider builders use resolveImageURL instead.
func (img ImageRef) AsDataURI() string {
	if img.URL != "" {
		return img.URL
	}
	return fmt.Sprintf("data:%s;base64,%s", img.MediaType, img.Data)
}

// resolveImageURL returns a provider-ready URL. File-backed refs are read and
// encoded only at the transport boundary; text-only requests never touch the
// image file.
func resolveImageURL(img *ImageRef) (string, error) {
	if img == nil {
		return "", fmt.Errorf("image part with nil Image")
	}
	if img.URL != "" {
		return img.URL, nil
	}
	data := img.Data
	if data == "" && img.Path != "" {
		buf, err := os.ReadFile(img.Path)
		if err != nil {
			return "", fmt.Errorf("read image %q: %w", img.Path, err)
		}
		data = base64.StdEncoding.EncodeToString(buf)
	}
	if img.MediaType == "" || data == "" {
		return "", fmt.Errorf("image part: incomplete (need URL, Data, or Path+MediaType)")
	}
	return "data:" + img.MediaType + ";base64," + data, nil
}

// Validate returns nil if the message is structurally sound.
func (m Message) Validate() error {
	switch m.Role {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
	default:
		return fmt.Errorf("llm.Message: unknown role %q", m.Role)
	}
	if m.Role == RoleTool && m.ToolCallID == "" {
		return fmt.Errorf("llm.Message(%s): missing ToolCallID", m.Role)
	}
	if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
		for i, tc := range m.ToolCalls {
			if tc.ID == "" {
				return fmt.Errorf("llm.Message tool call %d: empty ID", i)
			}
			if tc.Name == "" {
				return fmt.Errorf("llm.Message tool call %d: empty Name", i)
			}
		}
	}
	if len(m.Parts) == 0 && m.Content == "" && m.Role != RoleTool && len(m.ToolCalls) == 0 {
		return fmt.Errorf("llm.Message(%s): empty content, no parts, and no tool calls", m.Role)
	}
	for i, p := range m.Parts {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("llm.Message part %d: %w", i, err)
		}
	}
	return nil
}

// Validate returns nil if the part has the minimum required fields.
func (p ContentPart) Validate() error {
	switch p.Type {
	case PartTypeText:
		if p.Text == "" {
			return fmt.Errorf("text part: empty text")
		}
	case PartTypeImage:
		if p.Image == nil {
			return fmt.Errorf("image part: nil image")
		}
		if p.Image.URL == "" && p.Image.Data == "" && p.Image.Path == "" {
			return fmt.Errorf("image part: URL, Data, and Path all empty")
		}
		if p.Image.URL == "" && p.Image.MediaType == "" {
			return fmt.Errorf("image part: MediaType required when Data or Path is set")
		}
	case "":
		return fmt.Errorf("part: empty type")
	default:
		return fmt.Errorf("part: unknown type %q", p.Type)
	}
	return nil
}

// HasImage reports whether the message contains any image part.
func (m Message) HasImage() bool {
	for _, p := range m.Parts {
		if p.Type == PartTypeImage {
			return true
		}
	}
	return false
}

// DormantImages returns a deep-enough copy with all image refs marked inactive.
// Durable session/history writes use this so a resumed conversation never
// automatically resends old pixels. The file reference + media id remain.
func (m Message) DormantImages() Message {
	if len(m.Parts) == 0 {
		return m
	}
	out := m
	out.Parts = append([]ContentPart(nil), m.Parts...)
	for i := range out.Parts {
		if out.Parts[i].Type != PartTypeImage || out.Parts[i].Image == nil {
			continue
		}
		copy := *out.Parts[i].Image
		copy.Active = false
		out.Parts[i].Image = &copy
	}
	return out
}

// TextOnly returns a copy of the message with image parts stripped
// and all text concatenated. Use it for models without vision support.
func (m Message) TextOnly() Message {
	out := Message{Role: m.Role, Name: m.Name, ToolCallID: m.ToolCallID}
	var b strings.Builder
	if m.Content != "" {
		b.WriteString(m.Content)
	}
	for _, p := range m.Parts {
		if p.Type == PartTypeText {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(p.Text)
		}
	}
	out.Content = b.String()
	return out
}

// _ ensures fmt is used (some build configs strip unused imports).
// (intentionally no `var _ = ...` here; if fmt becomes unused, remove it from the import)
