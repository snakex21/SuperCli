package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	"supercli/internal/ui/viewport"
)

func BenchmarkTUIActiveHistoryAssembly(b *testing.B) {
	for _, tc := range []struct{ history, current int }{{0, 4096}, {60, 4096}, {600, 4096}, {2400, 4096}, {2400, 32768}} {
		b.Run(fmt.Sprintf("%d/%d", tc.history, tc.current), func(b *testing.B) {
			p := NoColorPalette()
			m := Model{palette: p, chat: completedHistoryBenchmarkFixture(tc.history), width: 100, height: 36, viewport: viewport.New(100, 20), busy: true, spinner: spinner.New(spinner.WithSpinner(spinner.Spinner{Frames: []string{"spin-A", "spin-B"}}))}
			const line = "Generated current answer with **bold** and ordinary text. "
			m.current = strings.Repeat(line, tc.current/len(line)+1)[:tc.current]
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
