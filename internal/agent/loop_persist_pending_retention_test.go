package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"supercli/internal/llm"
)

type pendingRetentionWriter struct {
	failAll  bool
	failAt   int
	attempts int
	messages []llm.Message
}

func (w *pendingRetentionWriter) AppendMessage(_ context.Context, msg llm.Message) error {
	w.attempts++
	if w.failAll || w.attempts == w.failAt {
		return errors.New("store unavailable")
	}
	w.messages = append(w.messages, msg)
	return nil
}
func (w *pendingRetentionWriter) UpdateUsage(int, int) error { return nil }

func pendingRetentionMessages() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeText, Text: "inspect current image"}, {Type: llm.PartTypeImage, Image: &llm.ImageRef{Path: "current.png", MediaType: "image/png", ID: "current-image", Active: true}}}},
		{Role: llm.RoleAssistant, Parts: []llm.ContentPart{{Type: llm.PartTypeReasoning, Reasoning: &llm.ReasoningBlock{Format: llm.ReasoningResponses, Model: "fixture", Scope: "fixture-origin", Data: json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque continuation"}`)}}}, ToolCalls: []llm.ToolCall{{ID: "current-call", Name: "execute", Arguments: `{"command":"test current"}`}}},
		{Role: llm.RoleTool, Name: "execute", ToolCallID: "current-call", Content: "command_failed exit=1: preserve this failure"},
	}
}

func pendingRetentionPayloads(pending []pendingAppend) []llm.Message {
	if pending == nil {
		return nil
	}
	messages := make([]llm.Message, len(pending))
	for i, item := range pending {
		messages[i] = item.Message
	}
	return messages
}

func TestPersistPendingRecoveryReleasesPayloads(t *testing.T) {
	for _, mode := range []string{"append", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			writer := &pendingRetentionWriter{failAll: true}
			sink := make(chan Event, 8)
			loop := &Loop{writer: writer, extOut: sink}
			messages := pendingRetentionMessages()
			for _, msg := range messages {
				loop.persistAppend(context.Background(), msg)
			}
			backing := loop.persistHealth.pending
			loop.persistHealth.projectionDirty = true
			writer.failAll = false
			want := append([]llm.Message(nil), messages...)
			if mode == "append" {
				current := llm.Message{Role: llm.RoleUser, Content: "next turn"}
				loop.persistAppend(context.Background(), current)
				want = append(want, current)
			} else {
				loop.retryPendingAppends(context.Background())
			}
			if !reflect.DeepEqual(writer.messages, want) {
				t.Fatal("recovery changed FIFO or image/native/tool-pair payloads")
			}
			if loop.persistHealth.pending != nil {
				t.Fatalf("fully written retry buffer retains backing array: len=%d cap=%d", len(loop.persistHealth.pending), cap(loop.persistHealth.pending))
			}
			for i, msg := range backing {
				if !reflect.DeepEqual(msg, pendingAppend{}) {
					t.Fatalf("written slot %d still retains its payload", i)
				}
			}
			status := loop.PersistStatus()
			if status.Failures != len(messages) || status.Pending != 0 || status.Dropped != 0 || loop.persistHealth.outage || !status.ProjectionDirty {
				t.Fatalf("recovery changed health/dirty projection semantics: %+v", status)
			}
			notices := drainNotices(sink)
			if len(notices) != 2 {
				t.Fatalf("notices=%d, want one failure and one recovery", len(notices))
			}
		})
	}
}

func TestPersistPendingPartialRecoveryKeepsUnwrittenPayloads(t *testing.T) {
	writer := &pendingRetentionWriter{failAll: true}
	sink := make(chan Event, 8)
	loop := &Loop{writer: writer, extOut: sink}
	messages := pendingRetentionMessages()
	for _, msg := range messages {
		loop.persistAppend(context.Background(), msg)
	}
	backing := loop.persistHealth.pending
	loop.persistHealth.projectionDirty = true
	writer.failAll = false
	writer.failAt = writer.attempts + 2
	current := llm.Message{Role: llm.RoleUser, Content: "next turn"}
	loop.persistAppend(context.Background(), current)
	wantPending := append(append([]llm.Message(nil), messages[1:]...), current)
	if !reflect.DeepEqual(pendingRetentionPayloads(loop.persistHealth.pending), wantPending) {
		t.Fatal("partial recovery removed or changed unwritten tool-pair/current payloads")
	}
	if !reflect.DeepEqual(backing[0], pendingAppend{}) {
		t.Fatal("successfully written slot retains its image payload")
	}
	if !reflect.DeepEqual(writer.messages, messages[:1]) {
		t.Fatal("partial recovery wrote out of order or duplicated a message")
	}
	status := loop.PersistStatus()
	if status.Failures != 4 || status.Pending != 3 || status.Dropped != 0 || !loop.persistHealth.outage || !status.ProjectionDirty {
		t.Fatalf("partial recovery changed health semantics: %+v", status)
	}
	loop.retryPendingAppends(context.Background())
	want := append(append([]llm.Message(nil), messages...), current)
	if !reflect.DeepEqual(writer.messages, want) || loop.persistHealth.pending != nil {
		t.Fatal("shutdown retry did not preserve FIFO or release the completed buffer")
	}
	for i, msg := range backing {
		if !reflect.DeepEqual(msg, pendingAppend{}) {
			t.Fatalf("retired slot %d still retains its payload", i)
		}
	}
	if notices := drainNotices(sink); len(notices) != 2 {
		t.Fatalf("notices=%d, want one outage warning and one recovery", len(notices))
	}
}

func TestPersistPendingOverflowClearsOnlyExistingEviction(t *testing.T) {
	writer := &pendingRetentionWriter{failAll: true}
	sink := make(chan Event, 8)
	loop := &Loop{writer: writer, extOut: sink}
	fixture := pendingRetentionMessages()
	var messages []llm.Message
	for i := 0; i < persistPendingMax; i++ {
		msg := fixture[i%len(fixture)]
		msg.Content = fmt.Sprintf("queued %d", i)
		messages = append(messages, msg)
		loop.persistAppend(context.Background(), msg)
	}
	backing := loop.persistHealth.pending
	current := llm.Message{Role: llm.RoleUser, Content: "overflow turn"}
	loop.persistAppend(context.Background(), current)
	want := append(append([]llm.Message(nil), messages[1:]...), current)
	if !reflect.DeepEqual(pendingRetentionPayloads(loop.persistHealth.pending), want) {
		t.Fatal("overflow changed the existing oldest-only eviction/FIFO policy")
	}
	if !reflect.DeepEqual(backing[0], pendingAppend{}) {
		t.Fatal("evicted slot still retains its image payload")
	}
	status := loop.PersistStatus()
	if status.Pending != persistPendingMax || status.Dropped != 1 || status.Failures != persistPendingMax+1 {
		t.Fatalf("overflow changed counters: %+v", status)
	}
	writer.failAll = false
	loop.retryPendingAppends(context.Background())
	if !reflect.DeepEqual(writer.messages, want) || loop.persistHealth.pending != nil {
		t.Fatal("overflow recovery lost retained messages or kept its completed backing array")
	}
	notices := drainNotices(sink)
	if len(notices) != 2 {
		t.Fatalf("notices=%d, want one outage warning and one recovery", len(notices))
	}
}
