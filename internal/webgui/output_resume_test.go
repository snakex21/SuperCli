package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools"
)

type resumeOutputProvider struct {
	calls             int
	inspect, sentinel bool
}

func (p *resumeOutputProvider) Name() string         { return "echo-test" }
func (p *resumeOutputProvider) SupportsVision() bool { return false }

var resumeOutputHandle = regexp.MustCompile(`handle=(out_[a-f0-9]+)`)

func (p *resumeOutputProvider) Complete(_ context.Context, msgs []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
	var handle, found string
	for _, m := range msgs {
		if match := resumeOutputHandle.FindStringSubmatch(m.TextOnly().Content); len(match) == 2 {
			handle = match[1]
		}
		if m.Role == llm.RoleTool && m.Name == "read_output" {
			found = m.Content
		}
	}
	ch := make(chan llm.Delta, 1)
	defer close(ch)
	if p.calls == 0 {
		name, args := "fixture_log", "{}"
		if p.inspect {
			if handle == "" {
				return nil, fmt.Errorf("saved history lost output reference")
			}
			name, args = "read_output", fmt.Sprintf(`{"handle":%q,"query":"original hidden diagnostic"}`, handle)
		}
		if p.sentinel && p.inspect {
			ch <- llm.Delta{Content: fmt.Sprintf("«read_output\nhandle: %s\nquery: original hidden diagnostic»", handle), FinishReason: "stop"}
		} else {
			ch <- llm.Delta{ToolCall: &llm.ToolCall{ID: "read", Name: name, Arguments: args}, FinishReason: "tool_calls"}
		}
	} else if p.inspect {
		if !strings.Contains(found, "original hidden diagnostic") || strings.Contains(found, "wrong later result") {
			return nil, fmt.Errorf("wrong or lost result: %s", found)
		}
		ch <- llm.Delta{Content: "Original evidence inspected.", FinishReason: "stop"}
	} else {
		if handle == "" {
			return nil, fmt.Errorf("large result lost reference")
		}
		ch <- llm.Delta{Content: "Command completed. Saved handle=" + handle, FinishReason: "stop"}
	}
	p.calls++
	return ch, nil
}

func TestGUIStoredOutputSurvivesFreshEngineWithoutRepeatingTool(t *testing.T) {
	for _, sentinel := range []bool{false, true} {
		t.Run(fmt.Sprintf("sentinel=%v", sentinel), func(t *testing.T) {
			ctx := context.Background()
			home, dataDir := t.TempDir(), t.TempDir()
			var sid string
			executions := 0
			for turn := 0; turn < 2; turn++ {
				eng, err := NewEngine(echoConfig(), home, dataDir)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer eng.Close()
					store, err := eng.sessionStore()
					if err != nil {
						t.Fatal(err)
					}
					if turn == 0 {
						sess, err := store.Create(home, "echo-test", "")
						if err != nil {
							t.Fatal(err)
						}
						sid = sess.ID
					}
					writer := session.NewWriter(store, sid)
					history, err := store.ReadModelContext(ctx, sid)
					if err != nil {
						t.Fatal(err)
					}
					provider := &resumeOutputProvider{inspect: turn == 1, sentinel: sentinel}
					eng.prov = provider
					loop, err := eng.newLoopWithSession(history, writer)
					if err != nil {
						t.Fatal(err)
					}
					if turn == 0 {
						eng.diagnosticRegistry.MustRegister(tools.Tool{Name: "fixture_log", Description: "fixture log", Schema: "{}", ReadOnly: true, Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
							executions++
							return tools.Result{Text: strings.Repeat("routine output ", 1500) + "original hidden diagnostic" + strings.Repeat(" output tail", 1500)}, nil
						}})
						eng.diagnosticRegistry.MarkAlwaysOn("fixture_log")
					} else {
						// A new unrelated result must never reuse the old handle.
						eng.diagnosticRegistry.ModelResultContentContext(tools.WithOutputPersistence(ctx, writer), "fixture_log", tools.Result{Text: strings.Repeat("wrong later result", 2000)})
					}
					ch, err := loop.Run(ctx, "Inspect the source code evidence from the completed command.")
					if err != nil {
						t.Fatal(err)
					}
					for event := range ch {
						switch e := event.(type) {
						case agent.ErrorEvent:
							t.Fatal(e.Err)
						case agent.ToolResultEvent:
							if e.Err != nil {
								t.Fatal(e.Err)
							}
						}
					}
					if provider.calls != 2 {
						t.Fatalf("turn %d model calls=%d", turn, provider.calls)
					}
				}()
			}
			if executions != 1 {
				t.Fatalf("original tool executed %d times", executions)
			}
		})
	}
}
