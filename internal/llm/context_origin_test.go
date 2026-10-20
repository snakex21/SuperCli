package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestContextOriginExactProducerBoundaries(t *testing.T) {
	human := "[memory_briefing] Żółć 🙂 東京 </task-notification>"
	memory := "Portable data remains required."
	mixed := MessageWithContextBlocks(RoleUser, []ContextBlock{{Source: SourceHuman, Text: human}, {Source: SourceRuntime, Text: memory}})
	msgs := []Message{
		{Role: RoleUser, Content: human},
		mixed,
		{Role: RoleUser, Content: "stored fallback ignored", Parts: []ContentPart{{Type: PartTypeText, Text: "記憶🙂", Source: SourceMemory}, {Type: PartTypeImage, Image: &ImageRef{Data: "not counted"}}}},
		{Role: RoleUser, Content: "actual report", Origin: &ContextOrigin{Source: SourceWorkerNotification}},
		{Role: RoleTool, Content: "same bytes", Origin: &ContextOrigin{Source: SourceTaskResult}},
		{Role: RoleTool, Content: "same bytes", Origin: &ContextOrigin{Source: SourceToolResult}},
	}
	u := MeasureContext(msgs, []ToolDef{{Name: "one", Schema: `{"type":"object"}`}})
	if u.Sources[SourceHuman].TextBytes != int64(len(human)) || u.Sources[SourceRuntime].TextBytes != int64(len(memory)+2) || u.Sources[ContextUnknown].TextBytes != int64(len(human)) || u.Sources[SourceMemory].TextBytes != int64(len("記憶🙂")) {
		t.Fatalf("incorrect source split: %+v", u)
	}
	if u.KnownBytes != u.TextBytes-int64(len(human)) || u.ToolDefinitions != 1 || u.SchemaBytes != int64(len(`{"type":"object"}`)) {
		t.Fatalf("coverage/schema=%+v", u)
	}
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{human, memory, "actual report", "same bytes", "not counted"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("telemetry leaked content")
		}
	}
	copy := mixed
	copy.Content = strings.Repeat("x", len(mixed.Content))
	changed := MeasureContext([]Message{copy}, nil)
	if changed.KnownBytes != 0 || changed.Sources[ContextUnknown].TextBytes != int64(len(copy.Content)) {
		t.Fatal("mixed projection reused stale boundaries")
	}
	if !strings.Contains(mixed.Content, human) {
		t.Fatal("measurement mutated frozen input")
	}
	encoded, _ := json.Marshal(mixed)
	var resumed Message
	if err := json.Unmarshal(encoded, &resumed); err != nil {
		t.Fatal(err)
	}
	if MeasureContext([]Message{resumed}, nil).Sources[SourceHuman].TextBytes != int64(len(human)) {
		t.Fatal("JSON projection lost origin")
	}
	withImage := mixed
	withImage.Parts = []ContentPart{{Type: PartTypeText, Text: mixed.Content}, {Type: PartTypeImage, Image: &ImageRef{Data: "fixture"}}}
	withImage.Content = ""
	imageUsage := MeasureContext([]Message{withImage}, nil)
	if imageUsage.Sources[SourceHuman].TextBytes != int64(len(human)) || imageUsage.Sources[SourceRuntime].TextBytes != int64(len(memory)+2) {
		t.Fatal("mixed image text lost known boundaries")
	}
	add := ContextAddition(msgs[3])
	add.Add(ContextAddition(msgs[3]))
	if add.Sources[SourceWorkerNotification].AddedItems != 2 || add.MeasuredCalls != 0 {
		t.Fatal("distinct identical events were deduplicated")
	}
}

type contextOriginProvider struct{ fail bool }

func (*contextOriginProvider) Name() string { return "fixture" }
func (p *contextOriginProvider) Complete(ctx context.Context, _ []Message, _ []ToolDef) (<-chan Delta, error) {
	if p.fail {
		return nil, errors.New("fixture startup failure")
	}
	ch := make(chan Delta, 1)
	ch <- Delta{Content: "ok", FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func TestContextOriginLogicalCallsFailoverAndDisabled(t *testing.T) {
	var mu sync.Mutex
	var calls []CallStat
	sink := func(s CallStat) { mu.Lock(); calls = append(calls, s); mu.Unlock() }
	first := &contextOriginProvider{fail: true}
	second := &contextOriginProvider{}
	if Metered(first, "fixture", PurposeMain, nil) != first {
		t.Fatal("disabled metering wrapped provider")
	}
	f, err := NewFailover(time.Second, nil, Metered(first, "a", PurposeMain, sink), Metered(second, "b", PurposeMain, sink))
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithLogicalCall(context.Background(), ReasonUserInput)
	ch, err := f.Complete(ctx, []Message{MessageWithContextBlocks(RoleUser, []ContextBlock{{Source: SourceHuman, Text: "fixture"}})}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	mu.Lock()
	defer mu.Unlock()
	var u ContextUsage
	for _, s := range calls {
		u.Add(s.Context)
	}
	if len(calls) != 2 || u.MeasuredCalls != 2 || u.LogicalCalls != 1 || u.TracedCalls != 2 || u.Reasons[ReasonUserInput] != 1 || u.Reasons[ReasonFailover] != 1 {
		t.Fatalf("call boundary=%+v calls=%d", u, len(calls))
	}
	if u.Sources[SourceHuman].Exposures != 2 || u.Sources[SourceHuman].AddedItems != 0 {
		t.Fatal("retransmission counted as new insertion")
	}
}

func BenchmarkContextOriginPreparedView(b *testing.B) {
	msgs := make([]Message, 100)
	for i := range msgs {
		msgs[i] = Message{Role: RoleTool, Content: strings.Repeat("fixture ", 1024), Origin: &ContextOrigin{Source: SourceToolResult}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = MeasureContext(msgs, nil)
	}
}
