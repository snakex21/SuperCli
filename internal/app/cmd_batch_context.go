package app

import (
	"crypto/sha256"
	"sort"
	"strconv"
	"sync"

	"supercli/internal/llm"
	"supercli/internal/system/stats"
)

// batchContextProbe measures what the main loop actually sends (batch
// --trace-jsonl only): where the prompt weight sits and whether each
// request keeps the previous one as an unchanged prefix. A changed prefix
// defeats provider prompt caches and local prompt reuse, so every
// prefixBreak is prompt work paid again. Estimates use llm's calibrated
// token estimator; nothing about the prompt text leaves this process.
type batchContextProbe struct {
	mu        sync.Mutex
	prev      []string
	prevTail  bool
	prevTools string
	calls     []batchContextCall
	tools     []batchToolSize
}

type batchContextCall struct {
	System   int `json:"system"`
	ToolDefs int `json:"toolDefs"`
	History  int `json:"history"`
	// Tail is the request-only trailing system message (time stamp, live
	// context). It is rebuilt every request by design and sits after the
	// conversation, so replacing it never breaks the cached prefix.
	Tail     int `json:"tail"`
	Messages int `json:"messages"`
	// SharedPrefix counts leading messages identical to the previous main
	// request. PrefixBreak means the previous request is not a prefix of
	// this one (something earlier changed); ToolsChanged means the tool
	// definitions differ from the previous request.
	SharedPrefix     int     `json:"sharedPrefix"`
	PrefixBreak      bool    `json:"prefixBreak,omitempty"`
	ToolsChanged     bool    `json:"toolsChanged,omitempty"`
	Input            int     `json:"input,omitempty"`
	Cached           int     `json:"cached,omitempty"`
	TTFTMs           float64 `json:"ttftMs,omitempty"`
	PrefillTokPerSec float64 `json:"prefillTokPerSec,omitempty"`
}

type batchToolSize struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
}

type batchTraceContext struct {
	Calls        []batchContextCall `json:"calls"`
	Tools        []batchToolSize    `json:"tools,omitempty"`
	PrefixBreaks int                `json:"prefixBreaks"`
	ToolsChanged int                `json:"toolsChanged"`
}

func newBatchContextProbe(enabled bool) *batchContextProbe {
	if !enabled {
		return nil
	}
	return &batchContextProbe{}
}

func (p *batchContextProbe) observe(purpose string, msgs []llm.Message, tools []llm.ToolDef) {
	if p == nil || purpose != llm.PurposeMain {
		return
	}
	digests := make([]string, len(msgs))
	for i, m := range msgs {
		digests[i] = batchMessageDigest(m)
	}
	toolsDigest := batchToolsDigest(tools)
	call := batchContextCall{Messages: len(msgs), ToolDefs: llm.EstimateRequestBreakdown(nil, tools).Tool}
	tail := len(msgs) > 1 && msgs[len(msgs)-1].Role == llm.RoleSystem
	leading := true
	for i, m := range msgs {
		tokens := llm.EstimateMessageTokens(m)
		if tail && i == len(msgs)-1 {
			call.Tail = tokens
			continue
		}
		if leading && m.Role == llm.RoleSystem {
			call.System += tokens
			continue
		}
		leading = false
		call.History += tokens
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for call.SharedPrefix < len(digests) && call.SharedPrefix < len(p.prev) && digests[call.SharedPrefix] == p.prev[call.SharedPrefix] {
		call.SharedPrefix++
	}
	if len(p.calls) > 0 {
		kept := len(p.prev)
		if p.prevTail {
			kept--
		}
		call.PrefixBreak = call.SharedPrefix < kept
		call.ToolsChanged = toolsDigest != p.prevTools
	}
	if p.tools == nil {
		p.tools = make([]batchToolSize, 0, len(tools))
		for _, tool := range tools {
			p.tools = append(p.tools, batchToolSize{Name: tool.Name, Tokens: llm.EstimateRequestBreakdown(nil, []llm.ToolDef{tool}).Tool})
		}
		sort.SliceStable(p.tools, func(i, j int) bool { return p.tools[i].Tokens > p.tools[j].Tokens })
	}
	p.prev, p.prevTail, p.prevTools = digests, tail, toolsDigest
	p.calls = append(p.calls, call)
}

// report pairs the observed requests with the provider-reported main calls.
// The main loop calls the model sequentially, so both lists share one order.
func (p *batchContextProbe) report(calls []stats.Call) *batchTraceContext {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := &batchTraceContext{Calls: append([]batchContextCall(nil), p.calls...), Tools: p.tools}
	i := 0
	for _, c := range calls {
		if c.Purpose != llm.PurposeMain || i >= len(out.Calls) {
			continue
		}
		row := &out.Calls[i]
		row.Input, row.Cached = c.TokensIn, c.TokensCached
		row.TTFTMs = float64(c.TTFTUs) / 1000
		row.PrefillTokPerSec = c.PrefillTokensPerSecond
		i++
	}
	for _, c := range out.Calls {
		if c.PrefixBreak {
			out.PrefixBreaks++
		}
		if c.ToolsChanged {
			out.ToolsChanged++
		}
	}
	return out
}

func batchMessageDigest(m llm.Message) string {
	h := sha256.New()
	w := func(s string) { h.Write([]byte(s)); h.Write([]byte{0}) }
	w(string(m.Role))
	w(m.Name)
	w(m.ToolCallID)
	w(m.Content)
	for _, part := range m.Parts {
		w(string(part.Type))
		w(part.Text)
		if part.Image != nil {
			w(part.Image.ID)
			w(part.Image.Path)
			w(part.Image.URL)
			w(strconv.FormatBool(part.Image.Active))
		}
		if part.Reasoning != nil {
			w(part.Reasoning.Format)
			h.Write(part.Reasoning.Data)
			h.Write([]byte{0})
		}
	}
	for _, tc := range m.ToolCalls {
		w(tc.ID)
		w(tc.Name)
		w(tc.Arguments)
	}
	return string(h.Sum(nil))
}

func batchToolsDigest(tools []llm.ToolDef) string {
	h := sha256.New()
	for _, tool := range tools {
		for _, s := range []string{tool.Name, tool.Description, tool.Schema} {
			h.Write([]byte(s))
			h.Write([]byte{0})
		}
	}
	return string(h.Sum(nil))
}
