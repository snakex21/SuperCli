package llm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync/atomic"
)

// ContextSource is host provenance, never inferred from roles or prompt text.
// Unknown includes legacy rows and projections without retained provenance.
type ContextSource string

const (
	ContextUnknown           ContextSource = "unknown"
	SourceHuman              ContextSource = "human"
	SourceWorkerNotification ContextSource = "worker_notification"
	SourceTaskResult         ContextSource = "task_result"
	SourceToolResult         ContextSource = "tool_result"
	SourceMemory             ContextSource = "memory"
	SourceRuntime            ContextSource = "runtime"
	SourceGoalState          ContextSource = "goal_state"
	SourceInstructions       ContextSource = "instructions"
	SourceModel              ContextSource = "model"
)

func knownSource(source ContextSource) ContextSource {
	switch source {
	case SourceHuman, SourceWorkerNotification, SourceTaskResult, SourceToolResult, SourceMemory, SourceRuntime, SourceGoalState, SourceInstructions, SourceModel:
		return source
	default:
		return ContextUnknown
	}
}

type ContextBlock struct {
	Source ContextSource
	Text   string
}

type ContextSpan struct {
	Source ContextSource `json:"source"`
	Bytes  int           `json:"bytes"`
}

// ContextOrigin is an internal envelope. Provider serializers do not use it.
// Mixed messages carry exact producer boundaries; changed mixed text becomes
// unknown rather than inheriting stale byte ranges after a projection.
type ContextOrigin struct {
	// WorkerActivity belongs only to the canonical transcript/UI. Provider
	// serializers ignore this envelope; model histories strip this payload.
	WorkerActivity json.RawMessage `json:"worker_activity,omitempty"`
	// ToolPreviews are durable session-media paths of user-only tool preview
	// images (MCP image_mode=preview). Transcript/UI only, never model input.
	ToolPreviews []string      `json:"tool_previews,omitempty"`
	Source       ContextSource `json:"source,omitempty"`
	Spans        []ContextSpan `json:"spans,omitempty"`
	Digest       [32]byte      `json:"digest,omitzero"`
}

// WithoutWorkerActivity drops only transcript display data. Provenance and
// source spans remain intact, and the caller's canonical message is immutable.
func (m Message) WithoutWorkerActivity() Message {
	if m.Origin != nil && len(m.Origin.WorkerActivity) > 0 {
		copy := *m.Origin
		copy.WorkerActivity = nil
		m.Origin = &copy
	}
	return m
}

func MessageWithContextBlocks(role Role, blocks []ContextBlock) Message {
	var text strings.Builder
	var spans []ContextSpan
	for _, block := range blocks {
		if block.Text == "" {
			continue
		}
		n := len(block.Text)
		if text.Len() != 0 {
			text.WriteString("\n\n")
			n += 2
		}
		text.WriteString(block.Text)
		source := knownSource(block.Source)
		if len(spans) > 0 && spans[len(spans)-1].Source == source {
			spans[len(spans)-1].Bytes += n
		} else {
			spans = append(spans, ContextSpan{Source: source, Bytes: n})
		}
	}
	m := Message{Role: role, Content: text.String()}
	if len(spans) == 1 {
		m.Origin = &ContextOrigin{Source: spans[0].Source}
	} else if len(spans) > 1 {
		m.Origin = &ContextOrigin{Spans: spans, Digest: sha256.Sum256([]byte(m.Content))}
	}
	return m
}

// ContextMeasure counts text only. Bytes are UTF-8, not tokens, HTTP bytes or
// monetary cost. Exposures count producer elements, Calls count containing calls.
type ContextMeasure struct {
	AddedItems int64 `json:"added_items,omitempty"`
	AddedBytes int64 `json:"added_bytes,omitempty"`
	Exposures  int64 `json:"exposures,omitempty"`
	TextBytes  int64 `json:"text_bytes,omitempty"`
	Calls      int64 `json:"calls,omitempty"`
}

type CallReason string

const (
	ReasonUnknown        CallReason = "unknown"
	ReasonUserInput      CallReason = "user_input"
	ReasonToolResults    CallReason = "tool_results"
	ReasonResume         CallReason = "resume"
	ReasonTransportRetry CallReason = "transport_retry"
	ReasonContextRetry   CallReason = "context_retry"
	ReasonFailover       CallReason = "failover"
	ReasonWorkerDelivery CallReason = "worker_delivery"
	ReasonDelegation     CallReason = "delegation"
)

// ContextUsage is additive and content-free. MeasuredCalls are Complete calls
// at Metered, NOT HTTP attempts (a transport can retry internally). LogicalCalls
// count only explicitly grouped loop calls; ungrouped helpers remain visible
// through MeasuredCalls-TracedCalls. SchemaBytes is the raw schema string size.
type ContextUsage struct {
	Sources         map[ContextSource]ContextMeasure `json:"sources,omitempty"`
	Reasons         map[CallReason]int64             `json:"reasons,omitempty"`
	MeasuredCalls   int64                            `json:"measured_calls,omitempty"`
	LogicalCalls    int64                            `json:"logical_calls,omitempty"`
	TracedCalls     int64                            `json:"traced_calls,omitempty"`
	TextBytes       int64                            `json:"text_bytes,omitempty"`
	KnownBytes      int64                            `json:"known_bytes,omitempty"`
	ToolDefinitions int64                            `json:"tool_definition_exposures,omitempty"`
	SchemaBytes     int64                            `json:"schema_bytes,omitempty"`
}

func (u *ContextUsage) Add(other ContextUsage) {
	for source, v := range other.Sources {
		v.AddedItems = max(0, v.AddedItems)
		v.AddedBytes = max(0, v.AddedBytes)
		v.Exposures = max(0, v.Exposures)
		v.TextBytes = max(0, v.TextBytes)
		v.Calls = max(0, v.Calls)
		if v == (ContextMeasure{}) {
			continue
		}
		if u.Sources == nil {
			u.Sources = make(map[ContextSource]ContextMeasure)
		}
		source = knownSource(source)
		p := u.Sources[source]
		p.AddedItems += v.AddedItems
		p.AddedBytes += v.AddedBytes
		p.Exposures += v.Exposures
		p.TextBytes += v.TextBytes
		p.Calls += v.Calls
		u.Sources[source] = p
	}
	for reason, n := range other.Reasons {
		if n <= 0 {
			continue
		}
		if u.Reasons == nil {
			u.Reasons = make(map[CallReason]int64)
		}
		u.Reasons[knownReason(reason)] += n
	}
	u.MeasuredCalls += other.MeasuredCalls
	u.LogicalCalls += other.LogicalCalls
	u.TracedCalls += other.TracedCalls
	u.TextBytes += other.TextBytes
	u.KnownBytes += other.KnownBytes
	u.ToolDefinitions += other.ToolDefinitions
	u.SchemaBytes += other.SchemaBytes
}

func (u ContextUsage) Empty() bool {
	if u.MeasuredCalls != 0 || u.LogicalCalls != 0 || u.TracedCalls != 0 || u.TextBytes != 0 || u.KnownBytes != 0 || u.ToolDefinitions != 0 || u.SchemaBytes != 0 {
		return false
	}
	for _, v := range u.Sources {
		if v != (ContextMeasure{}) {
			return false
		}
	}
	for _, n := range u.Reasons {
		if n != 0 {
			return false
		}
	}
	return true
}

func CloneContextUsage(u *ContextUsage) *ContextUsage {
	if u == nil || u.Empty() {
		return nil
	}
	copy := &ContextUsage{}
	copy.Add(*u)
	return copy
}

func MeasureContext(messages []Message, tools []ToolDef) ContextUsage {
	u := ContextUsage{MeasuredCalls: 1}
	add := func(source ContextSource, n int) {
		if n == 0 {
			return
		}
		source = knownSource(source)
		if u.Sources == nil {
			u.Sources = make(map[ContextSource]ContextMeasure)
		}
		v := u.Sources[source]
		v.Exposures++
		v.TextBytes += int64(n)
		v.Calls = 1
		u.Sources[source] = v
		u.TextBytes += int64(n)
		if source != ContextUnknown {
			u.KnownBytes += int64(n)
		}
	}
	addSpans := func(origin *ContextOrigin, text string) bool {
		if origin == nil || len(origin.Spans) == 0 || origin.Digest != sha256.Sum256([]byte(text)) {
			return false
		}
		total := 0
		for _, span := range origin.Spans {
			if span.Bytes < 0 || span.Bytes > len(text)-total {
				return false
			}
			total += span.Bytes
		}
		if total != len(text) {
			return false
		}
		for _, span := range origin.Spans {
			add(span.Source, span.Bytes)
		}
		return true
	}
	for _, m := range messages {
		source := ContextUnknown
		if m.Origin != nil {
			source = m.Origin.Source
		}
		// Native tool-result builders send Content, not multimodal Parts.
		if len(m.Parts) > 0 && m.Role != RoleTool {
			textParts := 0
			if m.Origin != nil && len(m.Origin.Spans) > 0 {
				for _, p := range m.Parts {
					if p.Type == PartTypeText {
						textParts++
					}
				}
			}
			for _, p := range m.Parts {
				if p.Type == PartTypeText {
					if p.Source == "" && textParts == 1 && addSpans(m.Origin, p.Text) {
						continue
					}
					partSource := source
					if p.Source != "" {
						partSource = p.Source
					}
					add(partSource, len(p.Text))
				}
			}
			continue
		}
		if m.Origin != nil && len(m.Origin.Spans) > 0 {
			if addSpans(m.Origin, m.Content) {
				continue
			}
			source = ContextUnknown
		}
		add(source, len(m.Content))
	}
	u.ToolDefinitions = int64(len(tools))
	for _, tool := range tools {
		u.SchemaBytes += int64(len(tool.Schema))
	}
	return u
}

func ContextAddition(m Message) ContextUsage {
	u := MeasureContext([]Message{m}, nil)
	for source, v := range u.Sources {
		u.Sources[source] = ContextMeasure{AddedItems: v.Exposures, AddedBytes: v.TextBytes}
	}
	u.MeasuredCalls, u.TextBytes, u.KnownBytes = 0, 0, 0
	return u
}

type callTraceKey struct{}
type callReasonKey struct{}
type callTrace struct{ attempts atomic.Int64 }

func knownReason(reason CallReason) CallReason {
	switch reason {
	case ReasonUserInput, ReasonToolResults, ReasonResume, ReasonTransportRetry, ReasonContextRetry, ReasonFailover, ReasonWorkerDelivery, ReasonDelegation:
		return reason
	default:
		return ReasonUnknown
	}
}

func WithCallReason(ctx context.Context, reason CallReason) context.Context {
	return context.WithValue(ctx, callReasonKey{}, knownReason(reason))
}
func CallReasonFromContext(ctx context.Context) CallReason {
	reason, _ := ctx.Value(callReasonKey{}).(CallReason)
	return knownReason(reason)
}
func WithLogicalCall(ctx context.Context, reason CallReason) context.Context {
	return context.WithValue(WithCallReason(ctx, reason), callTraceKey{}, &callTrace{})
}

func measuredContext(ctx context.Context, messages []Message, tools []ToolDef) ContextUsage {
	u := MeasureContext(messages, tools)
	u.Reasons = map[CallReason]int64{CallReasonFromContext(ctx): 1}
	if trace, _ := ctx.Value(callTraceKey{}).(*callTrace); trace != nil {
		u.TracedCalls = 1
		if trace.attempts.Add(1) == 1 {
			u.LogicalCalls = 1
		}
	}
	return u
}
