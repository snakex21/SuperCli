package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/agent"
	"supercli/internal/llm"
)

func transcriptEventFixture(expanded, top bool) Model {
	m := New(Options{NoColor: true, Language: "en"})
	m.width, m.height = 100, 36
	m.viewport.Width, m.chat.width = 100, 100
	m.toolExpanded = expanded
	for i := 0; i < 30; i++ {
		m.chat.addSystem(fmt.Sprintf("Earlier record %02d — Żółć 中文 😀", i))
	}
	m.toolNames = map[string]string{"finished": "read_file", "pending": "search_code"}
	m.lastToolName = "other_tool"
	m.refreshTranscript()
	if top {
		m.viewport.GotoTop()
	}
	return m
}

// Compare the real event handler with the independently constructed canonical
// result. No legacy formatter or second event-handler implementation is needed.
func TestToolResultEventPreservesCanonicalStateAndViewport(t *testing.T) {
	for _, sample := range []struct {
		name, output, wantOutput, errText string
		images                            []llm.ImageRef
	}{
		{name: "raw", output: "First line\nŻółć 中文 😀\r\nthird", wantOutput: "First line\nŻółć 中文 😀\r\nthird"},
		{name: "json", output: `{"stdout":"First\nŻółć 中文 😀","stderr":"","exit_code":0}`, wantOutput: `{"stdout":"First\nŻółć 中文 😀","stderr":"","exit_code":0}`},
		{name: "empty"},
		{name: "failure", output: "kept diagnostic", wantOutput: "kept diagnostic", errText: "fixture failure"},
		{name: "image", output: "loaded", wantOutput: "loaded\nImage: portable/image.png", images: []llm.ImageRef{{Path: ""}, {Path: "portable/image.png"}}},
	} {
		for _, expanded := range []bool{false, true} {
			for _, top := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/expanded=%v/top=%v", sample.name, expanded, top), func(t *testing.T) {
					m := transcriptEventFixture(expanded, top)
					want := transcriptEventFixture(expanded, top)
					want.chat.addToolResult("read_file", sample.wantOutput, sample.errText)
					want.hasTranscript = true
					delete(want.toolNames, "finished")
					e := agent.ToolResultEvent{ID: "finished", Output: sample.output, Images: sample.images}
					if sample.errText != "" {
						e.Err = errors.New(sample.errText)
						want.toolActivity.errors++
					}
					want.refreshTranscript()
					next, cmd := m.handleAgentEvent(e)
					m = next.(Model)
					if cmd != nil || !m.hasTranscript || m.toolActivity.errors != want.toolActivity.errors || !reflect.DeepEqual(m.chat.msgs, want.chat.msgs) || !reflect.DeepEqual(m.toolNames, want.toolNames) {
						t.Fatal("tool result changed canonical content, call attribution, presence or continuation")
					}
					if m.viewport.View() != want.viewport.View() || m.viewport.YOffset != want.viewport.YOffset || m.viewport.TotalLineCount() != want.viewport.TotalLineCount() || m.viewport.AtBottom() != want.viewport.AtBottom() {
						t.Fatal("tool result changed viewport bytes or scroll behavior")
					}
				})
			}
		}
	}
}

func TestWorkerToolCallMarksPresenceWithoutAddingChatMessage(t *testing.T) {
	m := New(Options{NoColor: true, Language: "en"})
	m.width, m.height = 100, 36
	e := agent.WorkerProgressEvent{TaskID: "worker-1", Agent: "code", Kind: "tool_call", Tool: "read_file", Args: `{"file":"portable/example.go"}`}
	next, _ := m.handleAgentEvent(e)
	m = next.(Model)
	if !m.hasTranscript || len(m.chat.msgs) != 0 {
		t.Fatal("worker tool call lost presence or added a previously invisible transcript line")
	}
	if len(m.workerViews) != 1 || m.workerViews[0].status != "running" || m.workerViews[0].activity != "read_file" || !strings.Contains(strings.Join(m.workerPanelLines(), "\n"), "read_file") {
		t.Fatal("worker tool call no longer updates the visible worker panel")
	}
}

func TestTerminalEventsPreservePresenceAndReasoning(t *testing.T) {
	t.Run("empty completion", func(t *testing.T) {
		m := New(Options{NoColor: true, Language: "en"})
		e := agent.DoneEvent{Usage: agent.Usage{Input: 2, Output: 3}, GenerationTokens: 4, GenerationDuration: time.Second}
		next, cmd := m.handleAgentEvent(e)
		m = next.(Model)
		if !m.hasTranscript || cmd == nil || len(m.chat.msgs) != 1 || m.chat.msgs[0].text != m.marker.DoneEst(2, 3, false) || m.chat.msgs[0].generationSpeed != 4 {
			t.Fatal("empty completion lost its presence, completion marker, throughput or close continuation")
		}
	})
	t.Run("reasoning then error", func(t *testing.T) {
		m := New(Options{NoColor: true, Language: "en"})
		for _, event := range []agent.Event{agent.ReasoningEvent{Text: "Plan Żółć 中文"}, agent.MessageEvent{Text: "Exact answer"}, agent.ErrorEvent{Err: errors.New("fixture failure")}} {
			next, _ := m.handleAgentEvent(event)
			m = next.(Model)
		}
		if !m.hasTranscript || m.current != "" || m.currentBuffer != nil || m.reasoningOpen || len(m.chat.msgs) != 2 || m.chat.lastAssistant() != "<thinking>Plan Żółć 中文</thinking>\nExact answer" || m.chat.msgs[1].text != m.marker.Error(errors.New("fixture failure")) {
			t.Fatal("error changed completed reasoning, answer, error marker or stream cleanup")
		}
	})
}

func TestRestoredAndDocumentEmptyRecordsPreservePresence(t *testing.T) {
	for _, sample := range []struct {
		name     string
		messages []llm.Message
		present  bool
		count    int
	}{
		{name: "no records"},
		{name: "empty user", messages: []llm.Message{{Role: llm.RoleUser}}, present: true, count: 1},
		{name: "empty tool", messages: []llm.Message{{Role: llm.RoleTool, Name: "read_file"}}, present: true, count: 1},
		{name: "blank assistant", messages: []llm.Message{{Role: llm.RoleAssistant, Content: " \n\t"}}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			m := New(Options{NoColor: true, Language: "en"})
			m.appendLine("old conversation")
			m.applyResumedTranscript(&resumedTranscript{ID: "fixture", Messages: sample.messages, Seqs: make([]int, len(sample.messages))})
			if m.hasTranscript != sample.present || len(m.chat.msgs) != sample.count || m.loadedSessionID != "fixture" {
				t.Fatal("restore changed empty-record presence or retained old records")
			}
			next, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 36})
			m = next.(Model)
			if m.hasTranscript != sample.present || len(m.chat.msgs) != sample.count {
				t.Fatal("resize changed restored transcript presence")
			}
		})
	}
	m := New(Options{NoColor: true, Language: "en"})
	next, _ := m.Update(slashResultMsg{Document: true, Local: true})
	m = next.(Model)
	if !m.hasTranscript || len(m.chat.msgs) != 1 || m.chat.msgs[0].role != roleDocument || m.chat.msgs[0].text != "" {
		t.Fatal("empty command document lost canonical content or presence")
	}
}
