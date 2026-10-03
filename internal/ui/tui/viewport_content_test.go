package tui

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func viewportRepeatFixture(b testing.TB, count int) Model {
	b.Helper()
	m := New(Options{NoColor: true, Language: "en"})
	m.width, m.height = 100, 36
	m.viewport.Width = 100
	m.input.SetWidth(100)
	m.chat.width = 100
	for i := 0; i < count; i++ {
		switch i % 5 {
		case 0:
			m.chat.addUser(fmt.Sprintf("Inspect result %d — Żółć 中文 😀", i))
		case 1:
			m.chat.addAssistant("<thinking>Check carefully.</thinking>Ready **verified**.\n\n```go\nfunc main() {}\n```")
		case 2:
			m.chat.addToolResult("read_file", strings.Repeat("Line with data and Unicode 中文. ", 4)+"\nnext line", "")
		case 3:
			m.chat.addSystem(fmt.Sprintf("operation %d completed", i))
		case 4:
			m.chat.addToolResult("ctx_execute", `{"stdout":"ok\nresult","stderr":"","exit_code":0}`, "")
		}
	}
	m.refreshTranscript()
	return m
}

func forceViewportRefresh(m *Model) {
	m.chat.current = m.current
	if m.chat.toolsExpanded != m.toolExpanded {
		m.chat.toolsExpanded = m.toolExpanded
		m.chat.completedDirty = true
	}
	spinner := m.streamSpinner()
	follow := m.viewport.AtBottom()
	m.resizeViewport()
	m.viewport.SetContent(m.chat.renderWithSpinner(m.palette, spinner))
	if follow {
		m.viewport.GotoBottom()
	}
}

func TestViewportRepeatParity(t *testing.T) {
	for _, count := range []int{0, 10, 500} {
		for _, top := range []bool{false, true} {
			m := viewportRepeatFixture(t, count)
			if top {
				m.viewport.GotoTop()
			}
			for step := 0; step < 15; step++ {
				switch step {
				case 2:
					m.chat.addUser("new user")
				case 3:
					m.chat.addToolResult("tool", "wide "+strings.Repeat("中文", 200), "")
				case 4:
					m.chat.toggleThinking()
				case 5:
					m.height = 20
				case 6:
					m.height = 45
				case 7:
					m.toolExpanded = !m.toolExpanded
				case 8:
					m.current = "response"
				case 9:
					m.current += " final"
				case 10:
					m.current = ""
				case 11:
					next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
					m = next.(Model)
				case 12:
					m.chat.addSystem("line with CRLF\r\nsecond line")
				case 13:
					m.viewport.YOffset = m.viewport.TotalLineCount() + 100
				}
				fresh := m
				forceViewportRefresh(&fresh)
				m.refreshTranscript()
				if got, want := m.viewport.View(), fresh.viewport.View(); got != want {
					t.Fatalf("count=%d top=%v step=%d changed viewport\n%s\n%s", count, top, step, ansi.Strip(got), ansi.Strip(want))
				}
				if m.viewport.YOffset != fresh.viewport.YOffset || m.viewport.TotalLineCount() != fresh.viewport.TotalLineCount() || m.viewport.AtBottom() != fresh.viewport.AtBottom() {
					t.Fatalf("count=%d top=%v step=%d changed scroll metadata", count, top, step)
				}
			}
			original := m
			copied := m
			copied.chat = newChat(60, "pl")
			copied.chat.addUser("different copied model")
			copied.current = ""
			copied.refreshTranscript()
			original.refreshTranscript()
			if !strings.Contains(ansi.Strip(copied.viewport.View()), "different copied model") || original.viewport.TotalLineCount() == copied.viewport.TotalLineCount() {
				t.Fatal("copied model's content was not independent")
			}
		}
	}
	// A replaced welcome viewport is routed through the actual update writer.
	m := New(Options{NoColor: true, Language: "en"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	m.chat.addUser("first chat after welcome")
	m.refreshTranscript()
	m.chat = newChat(80, "en")
	m.hasTranscript = false
	next, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	m.chat.addUser("first chat after welcome")
	m.refreshTranscript()
	if !strings.Contains(ansi.Strip(m.viewport.View()), "first chat after welcome") {
		t.Fatal("welcome replacement caused stale chat")
	}
}

func BenchmarkViewportRepeatedRefresh(b *testing.B) {
	for _, count := range []int{0, 50, 500} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			m := viewportRepeatFixture(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.refreshTranscript()
			}
			b.StopTimer()
			b.ReportMetric(float64(unsafe.Sizeof(Model{})), "model-bytes")
		})
	}
}

// Changed snapshots must keep the same rendering path; the equality guard is
// useful only when the final viewport bytes are identical.
func BenchmarkViewportChangingContent(b *testing.B) {
	for _, count := range []int{0, 500} {
		for _, mode := range []string{"append", "same-length-replacement"} {
			b.Run(fmt.Sprintf("%d/%s", count, mode), func(b *testing.B) {
				m := viewportRepeatFixture(b, count)
				prefix := strings.Repeat("Active stream paragraph. ", 64)
				m.current = prefix + "00000000"
				m.refreshTranscript()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if mode == "append" {
						m.appendStreamText(" next")
					} else {
						m.current = fmt.Sprintf("%s%08d", prefix, i+1)
					}
					m.refreshTranscript()
				}
			})
		}
	}
}

func TestViewportContentDoesNotRetainRawCRLFKey(t *testing.T) {
	m := viewportRepeatFixture(t, 0)
	m.setViewportContent("before\r\nafter")
	if m.viewportContentSet || m.viewportContent != "" {
		t.Fatal("CRLF normalization retained an additional raw transcript key")
	}
	if strings.Contains(m.viewport.View(), "\r") || !strings.Contains(m.viewport.View(), "after") {
		t.Fatal("viewport did not preserve Bubbles newline normalization")
	}
	m.setViewportContent("LF only\nnext")
	if !m.viewportContentSet || m.viewportContent != "LF only\nnext" {
		t.Fatal("LF content reuse was not restored after CRLF")
	}
}
