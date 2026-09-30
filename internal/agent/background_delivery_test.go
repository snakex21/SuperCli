package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
	"supercli/internal/tools/core"
	"sync"
	"testing"
)

func TestBackgroundNoticeWaitsForSafeHistoryBoundary(t *testing.T) {
	for _, boundary := range []string{"provider", "tool"} {
		for _, thin := range []bool{false, true} {
			name := boundary + "/native"
			if thin {
				name = boundary + "/thin"
			}
			t.Run(name, func(t *testing.T) {
				started, resume := make(chan struct{}), make(chan struct{})
				reg := tools.NewRegistry()
				writer := &recordingWriter{}
				provider := makeScriptedProvider("Finished.")
				if boundary == "provider" {
					provider.onCalled = func(n int) {
						if n == 0 {
							close(started)
							<-resume
						}
					}
				} else {
					reg.MustRegister(tools.Tool{Name: "fixture_read", Description: "read", Schema: "{}", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
						close(started)
						<-resume
						return tools.Result{Text: "source finding"}, nil
					}})
					reg.MarkAlwaysOn("fixture_read")
					first := []llm.Delta{{ToolCall: &llm.ToolCall{ID: "read", Name: "fixture_read", Arguments: "{}"}, FinishReason: "tool_calls"}}
					if thin {
						first = []llm.Delta{{Content: "«fixture_read»", FinishReason: "stop"}}
					}
					provider.scripts = [][]llm.Delta{first, {{Content: "Finished.", FinishReason: "stop"}}}
				}
				loop, err := NewLoop(LoopConfig{Provider: provider, Registry: reg, Writer: writer, ThinTools: thin})
				if err != nil {
					t.Fatal(err)
				}
				events := mustRun(t, loop, "Inspect the source.")
				<-started
				before := len(loop.Messages)
				notice := "<task-notification>WORKER_FINDING</task-notification>"
				loop.InjectUserMessage(context.Background(), notice)
				if len(loop.Messages) != before {
					t.Error("background producer modified the active conversation")
				}
				close(resume)
				for event := range events {
					if e, ok := event.(ErrorEvent); ok {
						t.Error(e.Err)
					}
				}
				wantCalls := int32(1)
				if boundary == "tool" {
					wantCalls = 2
				}
				if provider.calls != wantCalls {
					t.Errorf("notification added a model turn: %d", provider.calls)
				}
				noticeIndex, priorIndex := -1, -1
				for i, m := range loop.Messages {
					if m.Content == notice {
						if noticeIndex >= 0 {
							t.Error("duplicate notification")
						}
						noticeIndex = i
					}
					if boundary == "provider" && m.Role == llm.RoleAssistant {
						priorIndex = i
					}
					if boundary == "tool" && m.Role == llm.RoleTool {
						priorIndex = i
					}
				}
				if noticeIndex < 0 || priorIndex < 0 || noticeIndex <= priorIndex {
					t.Errorf("notice split an unfinished exchange: prior=%d notice=%d", priorIndex, noticeIndex)
				}
				if boundary == "tool" {
					found := false
					for _, m := range provider.reqs[1] {
						if strings.Contains(m.Content, "WORKER_FINDING") {
							found = true
						}
					}
					if !found {
						t.Error("next model step lost the worker's report")
					}
				}
				persisted := 0
				for _, m := range writer.messages {
					if m.Content == notice {
						persisted++
					}
				}
				if persisted != 1 {
					t.Errorf("persisted reports=%d", persisted)
				}
			})
		}
	}
}

func TestBackgroundNoticeConcurrentIdleDelivery(t *testing.T) {
	writer := &recordingWriter{}
	loop, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("unused"), Registry: tools.NewRegistry(), Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 64; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			loop.InjectUserMessage(context.Background(), fmt.Sprintf("notice-%d", i))
		}(i)
	}
	group.Wait()
	seen := map[string]bool{}
	for _, m := range loop.Messages {
		if seen[m.Content] {
			t.Fatalf("duplicate %s", m.Content)
		}
		seen[m.Content] = true
	}
	if len(seen) != 64 || len(writer.messages) != 64 || loop.sessionBusy.Load() || loop.backgroundPending.Load() {
		t.Fatalf("messages=%d persisted=%d busy=%v pending=%v", len(seen), len(writer.messages), loop.sessionBusy.Load(), loop.backgroundPending.Load())
	}
	for i, m := range writer.messages {
		if m.Content != loop.Messages[i].Content {
			t.Fatal("persisted chronology differs from memory")
		}
	}
}

func TestBackgroundNoticeArrivingDuringReleaseIsNotStranded(t *testing.T) {
	started, resume := make(chan struct{}), make(chan struct{})
	writer := &blockingNoticeWriter{recordingWriter: &recordingWriter{}, started: started, resume: resume}
	loop, err := NewLoop(LoopConfig{Provider: makeScriptedProvider("unused"), Registry: tools.NewRegistry(), Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { loop.InjectUserMessage(context.Background(), "first notice"); close(done) }()
	<-started
	loop.InjectUserMessage(context.Background(), "second notice")
	close(resume)
	<-done
	if len(loop.Messages) != 2 || len(writer.messages) != 2 || loop.Messages[1].Content != "second notice" || loop.sessionBusy.Load() || loop.backgroundPending.Load() {
		t.Fatal("late report was lost, duplicated or stranded")
	}
}

type blockingNoticeWriter struct {
	*recordingWriter
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (w *blockingNoticeWriter) AppendMessage(ctx context.Context, msg llm.Message) error {
	w.once.Do(func() { close(w.started); <-w.resume })
	return w.recordingWriter.AppendMessage(ctx, msg)
}

func TestBackgroundNoticeSurvivesCanceledRun(t *testing.T) {
	started, resume := make(chan struct{}), make(chan struct{})
	provider := makeScriptedProvider("Will be canceled.")
	provider.onCalled = func(int) { close(started); <-resume }
	writer := &recordingWriter{}
	loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	events, err := loop.Run(ctx, "work")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	loop.InjectUserMessage(context.Background(), "finished worker report")
	cancel()
	close(resume)
	for range events {
	}
	count := 0
	for _, m := range loop.Messages {
		if m.Content == "finished worker report" {
			count++
		}
	}
	if count != 1 || loop.sessionBusy.Load() || loop.backgroundPending.Load() {
		t.Fatal("cancellation lost a worker's completed work")
	}
	count = 0
	for _, m := range writer.messages {
		if m.Content == "finished worker report" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("completed report was not saved after cancellation")
	}
}

func TestBackgroundWorkerFinishingAfterResumeKeepsOriginalSession(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprint(persistent), func(t *testing.T) {
			ctx := context.Background()
			reg := tools.NewRegistry()
			started, resume := make(chan struct{}), make(chan struct{})
			report := strings.Repeat("Original worker report.\n", 500) + "DETAIL_ONLY_IN_ORIGINAL=8473\n" + strings.Repeat("final detail\n", 500)
			child := makeScriptedProvider(report)
			child.onCalled = func(int) { close(started); <-resume }
			var oldWriter, nextWriter SessionWriter
			var store *session.Store
			var oldID, nextID string
			if persistent {
				var err error
				store, err = session.OpenStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				oldSession, err := store.Create("old", "fixture", "")
				if err != nil {
					t.Fatal(err)
				}
				nextSession, err := store.Create("next", "fixture", "")
				if err != nil {
					t.Fatal(err)
				}
				oldID, nextID = oldSession.ID, nextSession.ID
				oldWriter, nextWriter = session.NewWriter(store, oldID), session.NewWriter(store, nextID)
			} else {
				oldWriter, nextWriter = &recordingWriter{}, &recordingWriter{}
			}
			parent, err := NewLoop(LoopConfig{Provider: child, Registry: reg, Writer: oldWriter})
			if err != nil {
				t.Fatal(err)
			}
			spec := NewSubAgentRegistry()
			MustRegisterAll(spec, BuiltinSubAgents())
			task, err := NewAgentTool(spec, parent, reg, child, nil, NewLoop)
			if err != nil {
				t.Fatal(err)
			}
			notices := make(chan Event, 16)
			parent.SetExternalSink(notices)
			result, err := task.execute(ctx, json.RawMessage(`{"prompt":"finish the original work","async":true}`))
			if err != nil || result.Err != nil {
				t.Fatalf("%v %v", err, result.Err)
			}
			<-started
			history := []llm.Message{{Role: llm.RoleUser, Content: "Different conversation."}}
			if err := parent.ResumeConversation(ctx, nextWriter, history, nil); err != nil {
				close(resume)
				t.Fatal(err)
			}
			close(resume)
			for {
				event := <-notices
				if _, ok := event.(WorkerNotificationEvent); ok {
					break
				}
			}
			for _, m := range parent.Messages {
				if strings.Contains(m.Content, "Original worker report") {
					t.Fatal("worker report contaminated the selected conversation")
				}
			}
			var oldMessages, nextMessages []llm.Message
			if persistent {
				read := func(id string) []llm.Message {
					rows, err := store.ReadMessages(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					var messages []llm.Message
					for _, row := range rows {
						message, err := row.ToMessage()
						if err != nil {
							t.Fatal(err)
						}
						messages = append(messages, message)
					}
					return messages
				}
				oldMessages, nextMessages = read(oldID), read(nextID)
			} else {
				oldMessages = oldWriter.(*recordingWriter).messages
				nextMessages = nextWriter.(*recordingWriter).messages
			}
			if len(oldMessages) != 1 || !strings.Contains(oldMessages[0].Content, "Original worker report") || len(nextMessages) != 0 {
				t.Fatalf("wrong session: old=%d next=%d", len(oldMessages), len(nextMessages))
			}
			if persistent {
				handle := core.StoredOutputHandle(oldMessages[0].Content)
				if handle == "" {
					t.Fatal("large report has no retrieval handle")
				}
				full, err := oldWriter.(tools.OutputPersistence).ReadToolOutput(ctx, handle)
				if err != nil || !strings.Contains(full, "DETAIL_ONLY_IN_ORIGINAL=8473") {
					t.Fatalf("old output lost: %v", err)
				}
				// Opaque handles can be followed across sessions. Deleting their
				// owning fixture session proves where the output was actually saved.
				if err := store.Delete(oldID); err != nil {
					t.Fatal(err)
				}
				if _, err := nextWriter.(tools.OutputPersistence).ReadToolOutput(ctx, handle); err == nil {
					t.Fatal("large report was owned by the next session")
				}
			}
		})
	}
}
