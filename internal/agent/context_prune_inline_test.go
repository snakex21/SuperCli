package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

type pruneArchivePersistence struct {
	tools.OutputPersistence
	saves, reads int
}

func (p *pruneArchivePersistence) SaveToolOutput(ctx context.Context, handle, text string) error {
	p.saves++
	return p.OutputPersistence.SaveToolOutput(ctx, handle, text)
}
func (p *pruneArchivePersistence) ReadToolOutput(ctx context.Context, handle string) (string, error) {
	p.reads++
	return p.OutputPersistence.ReadToolOutput(ctx, handle)
}

func TestPrunedInlineReadsRemainRecoverable(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, persist := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%t/persist=%t", thin, persist), func(t *testing.T) {
				ctx := context.Background()
				root := t.TempDir()
				reg := tools.NewRegistry()
				sourceReads := 0
				spec := tools.NewReadLines(root).Spec()
				execute := spec.Fn
				spec.Fn = func(ctx context.Context, args json.RawMessage) (tools.Result, error) {
					sourceReads++
					return execute(ctx, args)
				}
				reg.MustRegister(spec)
				reg.MarkAlwaysOn("read_lines")
				if thin {
					ensureWorkerDiscovery(reg)
				}
				var persistence tools.OutputPersistence
				var saved *pruneArchivePersistence
				var store *session.Store
				var writer *session.Writer
				if persist {
					var err error
					store, err = session.OpenStore(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = store.Close() })
					sess, err := store.Create("pruned-inline", "fixture", "")
					if err != nil {
						t.Fatal(err)
					}
					writer = session.NewWriter(store, sess.ID)
					saved = &pruneArchivePersistence{OutputPersistence: writer}
					persistence = saved
				}
				loop, err := NewLoop(LoopConfig{Provider: echoProvider("prune-inline"), Registry: reg, BaseDir: root, ToolOutputs: persistence, ThinTools: thin})
				if err != nil {
					t.Fatal(err)
				}
				loop.windowFor = func(string) int { return 2000 }
				loop.pruneProtect = 1
				var indexes []int
				var originals []string
				// More results than the 32-entry output LRU: one archive must keep them all.
				for i := 0; i < 40; i++ {
					name := fmt.Sprintf("evidence-%02d.txt", i)
					body := strings.Repeat("Saved observation: zażółć gęślą jaźń.\n", 35) + fmt.Sprintf("SNAPSHOT_%02d=731\n", i)
					if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
					args := fmt.Sprintf("{\"file\":%q,\"from\":1,\"to\":100}", name)
					call := llm.ToolCall{ID: fmt.Sprint(i), Name: "read_lines", Arguments: args}
					if thin {
						call.Name = "invoke_tool"
						call.Arguments = "{\"tool\":\"read_lines\",\"args\":" + args + "}"
					}
					result := loop.invoke(ctx, call, make(chan Event, 8))
					if result.failed || len(result.followUps) != 1 {
						t.Fatalf("read: %+v", result)
					}
					original := result.followUps[0].Content
					if handleInOutput(original) != "" {
						t.Fatal("fixture was not inline")
					}
					loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}, result.followUps[0])
					indexes = append(indexes, len(loop.Messages)-1)
					originals = append(originals, original)
					if err := os.Remove(filepath.Join(root, name)); err != nil {
						t.Fatal(err)
					}
				}
				loop.Messages = append(loop.Messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "active", Name: "read_lines", Arguments: "{}"}}}, llm.Message{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "active", Content: strings.Repeat("current evidence ", 100)})
				if saved != nil && saved.saves != 0 {
					t.Fatal("ordinary inline reads incurred persistence writes")
				}
				if writer != nil {
					loop.writer = writer
					for _, message := range loop.Messages {
						loop.persist(ctx, message)
					}
				}
				before := loop.EstimateVisibleTokens()
				reclaimed := loop.maybePruneToolResults(ctx, nil)
				if reclaimed <= 0 || reclaimed != before-loop.EstimateVisibleTokens() {
					t.Fatal("prune accounting failed")
				}
				if saved != nil && saved.saves != 1 {
					t.Fatalf("archive writes=%d, want one", saved.saves)
				}
				reader := loop
				restoredMarkers := make(map[string]string)
				if persist {
					restored, err := store.ReadModelContext(ctx, writer.SessionID())
					if err != nil {
						t.Fatal(err)
					}
					for _, message := range restored {
						if message.Role == llm.RoleTool {
							restoredMarkers[message.ToolCallID] = message.Content
						}
					}
					transcript, err := store.ReadMessages(ctx, writer.SessionID())
					if err != nil {
						t.Fatal(err)
					}
					for k, index := range indexes {
						if transcript[index].Content != originals[k] {
							t.Fatal("full UI transcript was rewritten")
						}
					}
					fresh := tools.NewRegistry()
					if thin {
						ensureWorkerDiscovery(fresh)
					}
					reader, err = NewLoop(LoopConfig{Provider: echoProvider("restored-inline"), Registry: fresh, ToolOutputs: persistence, ThinTools: thin})
					if err != nil {
						t.Fatal(err)
					}
				}
				handles := make(map[string]bool)
				markers := make([]string, len(indexes))
				for k, index := range indexes {
					marker := loop.Messages[index].Content
					if persist {
						if restoredMarkers[loop.Messages[index].ToolCallID] != marker {
							t.Fatal("projection lost the archive reference")
						}
						marker = restoredMarkers[loop.Messages[index].ToolCallID]
					}
					markers[k] = marker
					if !strings.HasPrefix(marker, pruneMarkerPrefix) {
						t.Fatalf("not pruned: %d", index)
					}
					_, raw, ok := strings.Cut(marker, "read_output ")
					if !ok {
						t.Fatalf("inline evidence no longer retrievable: %s", marker)
					}
					raw = strings.TrimSuffix(raw, "]")
					var args map[string]any
					if err := json.Unmarshal([]byte(raw), &args); err != nil {
						t.Fatal(err)
					}
					handles[args["handle"].(string)] = true
					call := llm.ToolCall{ID: fmt.Sprintf("recover-%d", k), Name: "read_output", Arguments: raw}
					if thin {
						call.Name = "invoke_tool"
						call.Arguments = "{\"tool\":\"read_output\",\"args\":" + raw + "}"
					}
					got := reader.invoke(ctx, call, make(chan Event, 8))
					if got.failed || len(got.followUps) != 1 || !strings.Contains(got.followUps[0].Content, originals[k]) {
						t.Fatalf("lost captured result %d: %+v", k, got)
					}
				}
				if len(handles) != 1 || sourceReads != 40 {
					t.Fatalf("handles=%d source reads=%d", len(handles), sourceReads)
				}
				if saved != nil && saved.reads != 1 {
					t.Fatalf("restored archive reads=%d", saved.reads)
				}
				loop.maybePruneToolResults(ctx, nil)
				for k, index := range indexes {
					if loop.Messages[index].Content != markers[k] {
						t.Fatal("pruned prefix rewritten")
					}
				}
				if saved != nil && saved.saves != 1 {
					t.Fatal("archive saved again without new pruning")
				}
				t.Logf("40 original reads, 1 archive, %d -> %d estimated tokens", before, loop.EstimateVisibleTokens())
			})
		}
	}
}
