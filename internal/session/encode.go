package session

import (
	"encoding/json"
	"fmt"

	"supercli/internal/llm"
)

// Encoded is the on-disk / SQL row representation of a single
// message. It is lossless: every llm.Message field can be
// reconstructed via ToMessage.
type Encoded struct {
	SessionID      string
	Seq            int
	Role           string
	Content        string
	PartsJSON      string // JSON []ContentPart, "" if no parts
	ToolCallID     string
	ToolCallsJSON  string // JSON []llm.ToolCall, "" if no tool calls
	Name           string
}

// ToMessage decodes the Encoded into an llm.Message.
func (e Encoded) ToMessage() (llm.Message, error) {
	m := llm.Message{
		Role:       llm.Role(e.Role),
		Content:    e.Content,
		Name:       e.Name,
		ToolCallID: e.ToolCallID,
	}
	if e.PartsJSON != "" {
		if err := json.Unmarshal([]byte(e.PartsJSON), &m.Parts); err != nil {
			return llm.Message{}, fmt.Errorf("session.Encoded.ToMessage: parts: %w", err)
		}
	}
	if e.ToolCallsJSON != "" {
		if err := json.Unmarshal([]byte(e.ToolCallsJSON), &m.ToolCalls); err != nil {
			return llm.Message{}, fmt.Errorf("session.Encoded.ToMessage: tool_calls: %w", err)
		}
	}
	if err := m.Validate(); err != nil {
		return llm.Message{}, fmt.Errorf("session.Encoded.ToMessage: validate: %w", err)
	}
	return m, nil
}

// FromMessage encodes an llm.Message into the column-shaped
// Encoded form. The SessionID and Seq are filled by the caller
// (or left zero when constructing a fresh row).
func FromMessage(m llm.Message) (Encoded, error) {
	if err := m.Validate(); err != nil {
		return Encoded{}, fmt.Errorf("session.FromMessage: %w", err)
	}
	out := Encoded{
		Role:    string(m.Role),
		Content: m.Content,
		Name:    m.Name,
	}
	if m.ToolCallID != "" {
		out.ToolCallID = m.ToolCallID
	}
	if len(m.Parts) > 0 {
		buf, err := json.Marshal(m.Parts)
		if err != nil {
			return Encoded{}, fmt.Errorf("session.FromMessage: parts: %w", err)
		}
		out.PartsJSON = string(buf)
	}
	if len(m.ToolCalls) > 0 {
		buf, err := json.Marshal(m.ToolCalls)
		if err != nil {
			return Encoded{}, fmt.Errorf("session.FromMessage: tool_calls: %w", err)
		}
		out.ToolCallsJSON = string(buf)
	}
	return out, nil
}

// Validate checks the row has the minimum required fields.
func (e Encoded) Validate() error {
	if e.Role == "" {
		return fmt.Errorf("session.Encoded: role is empty")
	}
	switch llm.Role(e.Role) {
	case llm.RoleSystem, llm.RoleUser, llm.RoleAssistant, llm.RoleTool:
	default:
		return fmt.Errorf("session.Encoded: invalid role %q", e.Role)
	}
	if e.Role == string(llm.RoleTool) && e.ToolCallID == "" {
		return fmt.Errorf("session.Encoded: tool role needs ToolCallID")
	}
	return nil
}
