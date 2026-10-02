package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/tools"
)

func TestConfirmationDispatchIntegration(t *testing.T) {
	for _, route := range []string{"keys", "key_batch", "input_batch"} {
		t.Run(route, func(t *testing.T) {
			dispatch := func(m Model, keys ...tea.KeyMsg) Model {
				if route == "keys" {
					for _, key := range keys {
						next, _ := m.Update(key)
						m = next.(Model)
					}
					return m
				}
				var message tea.Msg
				if route == "key_batch" {
					message = terminalKeyBatchMsg(keys)
				} else {
					messages := make(terminalInputBatchMsg, len(keys))
					for i, key := range keys {
						messages[i] = key
					}
					message = messages
				}
				next, _ := m.Update(message)
				return next.(Model)
			}
			for _, scenario := range []struct {
				name  string
				keys  []tea.KeyMsg
				allow bool
			}{
				{"quick_pick_then_default_cancel", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'2'}}, {Type: tea.KeyEnter}}, false},
				{"unread_cannot_allow", []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyEnter}}, false},
				{"read_to_end_then_allow", []tea.KeyMsg{{Type: tea.KeyEnd}, {Type: tea.KeyTab}, {Type: tea.KeyEnter}}, true},
				{"escape_cancels", []tea.KeyMsg{{Type: tea.KeyEsc}}, false},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					m := keyDispatchTestModel(t)
					m.input.SetValue("unsent draft")
					m.busy = true
					answers := make(chan tools.AskAnswer, 2)
					req := tools.AskRequest{ID: "trusted-action", Question: strings.Repeat("action detail\n", 40), Confirmation: true, Options: []tools.AskOption{{Label: "Cancel"}, {Label: "Allow once"}}, Respond: answers}
					next, _ := m.Update(askRequestMsg{req: req})
					m = next.(Model)
					if m.pendingAsk == nil || !m.pendingAsk.Confirmation || m.pendingAsk.cursor != 0 || m.input.Focused() {
						t.Fatal("public request dispatch lost confirmation mode")
					}
					m = dispatch(m, scenario.keys...)
					select {
					case answer := <-answers:
						if scenario.allow {
							if answer.Cancelled || len(answer.Selected) != 1 || answer.Selected[0] != "Allow once" {
								t.Fatalf("explicit allow: %+v", answer)
							}
						} else if !answer.Cancelled || len(answer.Selected) != 0 {
							t.Fatalf("unsafe approval: %+v", answer)
						}
					default:
						t.Fatal("confirmation did not respond through public dispatch")
					}
					select {
					case answer := <-answers:
						t.Fatalf("duplicate response: %+v", answer)
					default:
					}
					if m.pendingAsk != nil || m.mode != modeNormal || !m.input.Focused() || m.input.Value() != "unsent draft" || m.quitting {
						t.Fatal("response lost draft/focus or exited the application")
					}
				})
			}
			t.Run("ctrl_c_promotes_queued_confirmation", func(t *testing.T) {
				m := keyDispatchTestModel(t)
				m.input.SetValue("unsent draft")
				m.busy = true
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				runCancelled := false
				m.cancel.Arm(cancelRun, func() { runCancelled = true; cancel() })
				first, second := make(chan tools.AskAnswer, 2), make(chan tools.AskAnswer, 2)
				for i, ch := range []chan tools.AskAnswer{first, second} {
					req := tools.AskRequest{ID: string(rune('a' + i)), Question: "short action", Confirmation: true, Options: []tools.AskOption{{Label: "Cancel"}, {Label: "Allow once"}}, Respond: ch}
					next, _ := m.Update(askRequestMsg{req: req})
					m = next.(Model)
				}
				m = dispatch(m, tea.KeyMsg{Type: tea.KeyTab}, tea.KeyMsg{Type: tea.KeyCtrlC})
				select {
				case answer := <-first:
					if !answer.Cancelled || len(answer.Selected) != 0 {
						t.Fatalf("Ctrl+C approved: %+v", answer)
					}
				default:
					t.Fatal("Ctrl+C did not cancel current confirmation")
				}
				if m.pendingAsk == nil || m.pendingAsk.ID != "b" || !m.pendingAsk.Confirmation || m.pendingAsk.cursor != 0 || m.mode != modeAsking || m.input.Focused() {
					t.Fatal("queued confirmation lost trusted safe state")
				}
				m = dispatch(m, tea.KeyMsg{Type: tea.KeyEnter})
				select {
				case answer := <-second:
					if !answer.Cancelled || len(answer.Selected) != 0 {
						t.Fatalf("queued default Enter approved: %+v", answer)
					}
				default:
					t.Fatal("queued confirmation did not respond")
				}
				if runCancelled || ctx.Err() != nil || m.quitting || !m.busy || m.pendingAsk != nil || m.mode != modeNormal || !m.input.Focused() || m.input.Value() != "unsent draft" {
					t.Fatal("ask cancellation changed run lifecycle or composer")
				}
			})
		})
	}
	t.Run("mixed_input_resize_revokes_allow", func(t *testing.T) {
		m := keyDispatchTestModel(t)
		answers := make(chan tools.AskAnswer, 1)
		req := tools.AskRequest{ID: "resized-action", Question: "short action", Confirmation: true, Options: []tools.AskOption{{Label: "Cancel"}, {Label: "Allow once"}}, Respond: answers}
		next, _ := m.Update(askRequestMsg{req: req})
		m = next.(Model)
		next, _ = m.Update(terminalInputBatchMsg{tea.KeyMsg{Type: tea.KeyTab}, tea.WindowSizeMsg{Width: 23, Height: 9}, tea.KeyMsg{Type: tea.KeyEnter}})
		m = next.(Model)
		select {
		case answer := <-answers:
			if !answer.Cancelled || len(answer.Selected) != 0 {
				t.Fatalf("resize allowed hidden action: %+v", answer)
			}
		default:
			t.Fatal("mixed resize/Enter batch did not respond")
		}
		if m.width != 23 || m.height != 9 || m.pendingAsk != nil {
			t.Fatal("mixed input ordering or confirmation cleanup lost")
		}
	})
}
