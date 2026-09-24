package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (p *CodexProvider) responsesAPIName() string {
	if p.cfg.StandardResponsesAPI {
		return "responses"
	}
	return "codex"
}

// --- SSE event shape ---

type codexEvent struct {
	Type     string         `json:"type"`
	Delta    string         `json:"delta,omitempty"`
	Code     string         `json:"code,omitempty"`
	Message  string         `json:"message,omitempty"`
	Error    *codexError    `json:"error,omitempty"`
	Item     *codexItemEv   `json:"item,omitempty"`
	Response *codexRespMeta `json:"response,omitempty"`
}

type codexItemEv struct {
	Type      string `json:"type"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`
}

type codexRespMeta struct {
	Usage *codexUsage `json:"usage,omitempty"`
	Error *codexError `json:"error,omitempty"`
}

type codexUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
	// Cache-miss telemetry: the Responses API reports the cached
	// share of input_tokens (and the hidden reasoning share of
	// output_tokens) in detail objects. Nil when omitted.
	InputTokensDetails  *codexInputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *codexOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

type codexInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type codexOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type codexError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// --- request translation: chat-completions shape → Responses API ---

type codexRequest struct {
	Model             string          `json:"model"`
	Instructions      string          `json:"instructions,omitempty"`
	Input             []codexItem     `json:"input"`
	Tools             []codexToolDecl `json:"tools,omitempty"`
	ToolChoice        string          `json:"tool_choice"`
	ParallelToolCalls bool            `json:"parallel_tool_calls"`
	Store             bool            `json:"store"`
	Stream            bool            `json:"stream"`
	Include           []string        `json:"include"`
	// Reasoning carries the effort level the same way the Codex
	// CLI does ({"effort": "...", "summary": "auto"}). Omitted
	// when no effort is configured.
	Reasoning *codexReasoning `json:"reasoning,omitempty"`
}

// codexReasoning is the Responses API reasoning config.
type codexReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type codexItem struct {
	Raw  json.RawMessage `json:"-"`
	Type string          `json:"type"`
	// message items
	Role    string             `json:"role,omitempty"`
	Content []codexContentPart `json:"content,omitempty"`
	// function_call items
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	// function_call_output items
	Output string `json:"output,omitempty"`
}

type codexContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type codexToolDecl struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Strict      bool            `json:"strict"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// buildCodexRequest translates SuperCli's chat-completion-shaped
// history into Responses API items:
//
//   - LEADING system msgs → the top-level "instructions" field
//   - user/assistant      → {"type":"message","content":[input_text|output_text]}
//   - assistant ToolCall  → {"type":"function_call",...}
//   - tool results        → {"type":"function_call_output",...}
//
// Mid-conversation system messages (freshness stamp, thin preamble,
// reflection checkpoints) must NOT be hoisted into "instructions":
// that would move per-request volatile bytes to the prompt front and
// invalidate the server-side prompt cache every turn. The demote pass
// renders them in place as <system-reminder> user turns instead.
func buildCodexRequest(model string, msgs []Message, tools []ToolDef, vision bool) ([]byte, error) {
	effort := ReasoningEffortForModel(model)
	if effort == "none" {
		effort = ""
	}
	return buildCodexRequestWithEffort(model, msgs, tools, vision, effort)
}

func buildCodexRequestWithEffort(model string, msgs []Message, tools []ToolDef, vision bool, effort string) ([]byte, error) {
	msgs = repairToolCallIDs(demoteMidConversationSystemMessages(msgs))
	req := codexRequest{
		Model:      model,
		ToolChoice: "auto",
		Store:      false,
		Stream:     true,
		Include:    []string{},
	}
	if effort != "" {
		req.Reasoning = &codexReasoning{Effort: effort}
		if effort != "none" {
			req.Reasoning.Summary = "auto"
		}
	}
	for _, t := range tools {
		parameters, err := normalizeToolSchemaChecked(t.Schema)
		if err != nil {
			return nil, fmt.Errorf("tool %q schema: %w", t.Name, err)
		}
		req.Tools = append(req.Tools, codexToolDecl{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  parameters,
		})
	}
	var instructions []string
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			instructions = append(instructions, messageText(m))
		case RoleTool:
			req.Input = append(req.Input, codexItem{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: m.Content,
			})
		case RoleAssistant:
			for _, raw := range nativePayloads(m, ReasoningResponses, model) {
				req.Input = append(req.Input, codexItem{Raw: raw})
			}
			if text := messageText(m); text != "" {
				req.Input = append(req.Input, codexItem{
					Type: "message", Role: "assistant",
					Content: []codexContentPart{{Type: "output_text", Text: text}},
				})
			}
			for _, tc := range m.ToolCalls {
				req.Input = append(req.Input, codexItem{
					Type:      "function_call",
					Name:      tc.Name,
					Arguments: providerSafeToolArguments(tc.Arguments),
					CallID:    tc.ID,
				})
			}
		default: // user
			parts, err := codexUserParts(m, vision)
			if err != nil {
				return nil, err
			}
			req.Input = append(req.Input, codexItem{
				Type: "message", Role: "user", Content: parts,
			})
		}
	}
	req.Instructions = strings.Join(instructions, "\n\n")
	return json.Marshal(req)
}

// prepareStandardResponsesRequest adds the state-carrying fields used by
// Codex-compatible public Responses gateways. They are deliberately absent
// from the ChatGPT-subscription dialect above. Reasoning-only fields are sent
// only to model families that support them; a stable non-empty prompt cache
// key lets the gateway reuse the shared prefix across turns.
func prepareStandardResponsesRequest(body []byte, promptCacheKey string, reasoningModel bool, sampling Sampling) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	// Sampling is meaningless (and rejected) for reasoning models, so it
	// is only forwarded to the rest. Unset parameters stay absent.
	if !reasoningModel {
		if sampling.Temperature != nil {
			req["temperature"] = *sampling.Temperature
		}
		if sampling.TopP != nil {
			req["top_p"] = *sampling.TopP
		}
	}
	if reasoningModel {
		reasoning, _ := req["reasoning"].(map[string]any)
		if reasoning == nil {
			reasoning = make(map[string]any)
		}
		// Missing effort means provider default. Never replace an unset dial
		// (or a model absent from a name heuristic) with a fixed medium.
		// Ask every catalog-advertised reasoning model for the richest summary
		// the standard Responses API exposes. This is capability-driven; no
		// model-name allowlist is involved.
		if reasoning["effort"] != "none" {
			reasoning["summary"] = "detailed"
		}
		req["reasoning"] = reasoning
		req["include"] = []string{"reasoning.encrypted_content"}
	}
	if _, ok := req["tools"]; !ok {
		req["tools"] = []any{}
	}
	promptCacheKey = strings.TrimSpace(promptCacheKey)
	if promptCacheKey == "" {
		promptCacheKey = "supercli"
	}
	req["prompt_cache_key"] = promptCacheKey
	return json.Marshal(req)
}

// prepareOpenCodeZenResponsesRequest reshapes a standard Responses body into
// the dialect the real OpenCode CLI posts to /zen/v1/responses (captured
// from opencode 1.18.32 via mitmproxy, post_2/post_3, 2026-09-22).
//
// Deltas vs prepareStandardResponsesRequest:
//   - no top-level "instructions": leading system text becomes an input item
//     {"role":"developer","content":"<raw string>"} (string, not content parts)
//   - no "type" on message input items (user/assistant stay role+content only)
//   - no "parallel_tool_calls"
//   - "reasoning": only when /reasoning is set (user dial for quality vs speed);
//     omitted otherwise (CLI capture shape; edge defaults high)
//   - "max_output_tokens": 32000
//   - "include": ["reasoning.encrypted_content"] always
//   - "prompt_cache_key" = x-opencode-session (ses_… / process fallback)
//   - empty tools: omit "tools" and "tool_choice" entirely
//
// sessionID is normalized to the ses_+26hex wire shape; it must match the
// x-opencode-session header ApplyOpenCodeZenHeaders sets on the same request.
func prepareOpenCodeZenResponsesRequest(body []byte, sessionID string) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	delete(req, "parallel_tool_calls")
	delete(req, "reasoning")
	delete(req, "temperature")
	delete(req, "top_p")
	req["max_output_tokens"] = 32000
	req["include"] = []string{"reasoning.encrypted_content"}
	// /reasoning is the sole control: when set (e.g. xhigh for quality, low
	// for speed) send it through. When unset, leave the field omitted — same
	// as the real CLI capture; the edge then applies its own default (high).
	// Do not force a level here; the dial exists so the user steers tradeoffs.
	if e := ReasoningEffort(); e != "" && e != "none" {
		req["reasoning"] = map[string]any{"effort": e, "summary": "auto"}
	}

	if inst, ok := req["instructions"].(string); ok {
		delete(req, "instructions")
		if strings.TrimSpace(inst) != "" {
			input, _ := req["input"].([]any)
			if input == nil {
				input = []any{}
			}
			dev := map[string]any{"role": "developer", "content": inst}
			req["input"] = append([]any{dev}, input...)
		}
	}
	if input, ok := req["input"].([]any); ok {
		for _, raw := range input {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := item["type"].(string); t == "message" {
				delete(item, "type")
			}
		}
	}
	tools, hasTools := req["tools"].([]any)
	if !hasTools || len(tools) == 0 {
		delete(req, "tools")
		delete(req, "tool_choice")
	} else {
		// Free-tier gate: tools must contain BOTH "bash" AND "read"
		// (bisect: either alone → 403; pair → 200). Inject minimal
		// placeholders matching exp_S4_sc_bash_sc_read.json; returned
		// calls are rewritten to SuperCli's real tools in the agent.
		tools = ensureZenGateTools(tools)
		req["tools"] = tools
		req["tool_choice"] = "auto"
	}
	sessionID = normalizeZenSessionID(sessionID)
	req["prompt_cache_key"] = sessionID
	return json.Marshal(req)
}

// ensureZenGateTools appends the OpenCode Zen free-tier gate pair
// (bash, read) when absent. Schemas match the minimal accepted capture
// exp_S4_sc_bash_sc_read.json (672 B). Idempotent.
func ensureZenGateTools(tools []any) []any {
	var hasBash, hasRead bool
	for _, t := range tools {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		switch name, _ := m["name"].(string); name {
		case "bash":
			hasBash = true
		case "read":
			hasRead = true
		}
	}
	if !hasBash {
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        "bash",
			"description": "run a command",
			"strict":      false,
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"command": map[string]any{"type": "string"}},
				"required":   []string{"command"},
			},
		})
	}
	if !hasRead {
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        "read",
			"description": "Read a file",
			"strict":      false,
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"filePath": map[string]any{"type": "string"}},
				"required":   []string{"filePath"},
			},
		})
	}
	return tools
}

func patchCodexReasoningEffort(body []byte, effort string) ([]byte, bool) {
	if effort == "none" {
		effort = ""
	}
	return patchResponsesReasoningEffort(body, effort)
}

func patchResponsesReasoningEffort(body []byte, effort string) ([]byte, bool) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false
	}
	if effort == "" {
		delete(req, "reasoning")
	} else {
		reasoning, _ := req["reasoning"].(map[string]any)
		if reasoning == nil {
			reasoning = make(map[string]any)
		}
		reasoning["effort"] = effort
		if effort == "none" {
			delete(reasoning, "summary")
		} else if _, ok := reasoning["summary"]; !ok {
			reasoning["summary"] = "auto"
		}
		req["reasoning"] = reasoning
	}
	out, err := json.Marshal(req)
	return out, err == nil
}

// messageText flattens a message's text content.
func messageText(m Message) string {
	if len(m.Parts) == 0 {
		return m.Content
	}
	var first string
	size := 0
	for _, p := range m.Parts {
		if p.Type == PartTypeText {
			if size == 0 {
				first = p.Text
			}
			size += len(p.Text)
		}
	}
	// A single nonempty text part is already the final immutable string.
	if size == len(first) {
		return first
	}
	var b strings.Builder
	b.Grow(size)
	for _, p := range m.Parts {
		if p.Type == PartTypeText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// codexUserParts encodes a user message as input_text/input_image
// content parts. Unsupported images become a text placeholder so the model
// knows context was omitted instead of silently losing it.
func codexUserParts(m Message, vision bool) ([]codexContentPart, error) {
	if len(m.Parts) == 0 {
		return []codexContentPart{{Type: "input_text", Text: m.Content}}, nil
	}
	out := make([]codexContentPart, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Type {
		case PartTypeText:
			out = append(out, codexContentPart{Type: "input_text", Text: p.Text})
		case PartTypeImage:
			if !vision {
				out = append(out, codexContentPart{Type: "input_text", Text: imageInputOmittedPlaceholder})
				continue
			}
			url, err := resolveImageURL(p.Image)
			if err != nil {
				return nil, err
			}
			out = append(out, codexContentPart{Type: "input_image", ImageURL: url})
		default:
			return nil, fmt.Errorf("unknown part type %q", p.Type)
		}
	}
	if len(out) == 0 {
		out = append(out, codexContentPart{Type: "input_text", Text: m.Content})
	}
	return out, nil
}
