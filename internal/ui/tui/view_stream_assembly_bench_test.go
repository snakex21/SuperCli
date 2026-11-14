package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"supercli/internal/ui/viewport"
)

// activeHistoryFixture is a minimal Model for the streaming assembly path.
// Production builds every chat Model with New, which creates the textarea;
// a zero textarea.Model has a nil internal viewport, so the composer field
// must be initialised here too (resizeViewport -> fitInputHeight uses it).
func activeHistoryFixture(history, current int) Model {
	m := Model{palette: NoColorPalette(), chat: completedHistoryBenchmarkFixture(history), width: 100, height: 36,
		viewport: viewport.New(100, 20), input: textarea.New(), busy: true,
		spinner: spinner.New(spinner.WithSpinner(spinner.Spinner{Frames: []string{"spin-A", "spin-B"}}))}
	const line = "Generated current answer with **bold** and ordinary text. "
	m.current = strings.Repeat(line, current/len(line)+1)[:current]
	return m
}

// Benchmarks do not run under plain go test; keep the fixture exercised.
func TestActiveHistoryFixtureRefreshes(t *testing.T) {
	m := activeHistoryFixture(60, 4096)
	m.refreshTranscript()
	m.spinner, _ = m.spinner.Update(m.spinner.Tick())
	m.refreshTranscript()
	if !strings.Contains(m.viewport.View(), "Generated current answer") {
		t.Fatal("streaming answer missing from the viewport")
	}
}

func BenchmarkTUIActiveHistoryAssembly(b *testing.B) {
	for _, tc := range []struct{ history, current int }{{0, 4096}, {60, 4096}, {600, 4096}, {2400, 4096}, {2400, 32768}} {
		b.Run(fmt.Sprintf("%d/%d", tc.history, tc.current), func(b *testing.B) {
			m := activeHistoryFixture(tc.history, tc.current)
			m.refreshTranscript()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.spinner, _ = m.spinner.Update(m.spinner.Tick())
				m.refreshTranscript()
			}
		})
	}
}
