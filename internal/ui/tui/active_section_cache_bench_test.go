package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/agent"
)

var activeSectionBenchmarkSink string

func activeSectionBenchmarkModel(b *testing.B, size int) Model {
	b.Helper()
	m := New(Options{Home: b.TempDir(), NoColor: true, Language: "en"})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
	m = next.(Model)
	m.busy = true
	m.eventCh = make(chan agent.Event)
	m.spinner = spinner.New(spinner.WithSpinner(spinner.Spinner{Frames: []string{"spin-A", "spin-B"}, FPS: time.Second}))
	line := "Zażółć 中文 😀 result with **bold** and \x60code\x60 for inspection.\n"
	m.current = strings.Repeat(line, size/len(line)+1)[:size]
	m.refreshTranscript()
	return m
}

// The real timer path must repaint changed spinner frames even without a new
// provider delta. Returned commands are not executed; there are no timers,
// network, model calls, or physical terminal writes in this preparation fixture.
func BenchmarkTUISpinnerOnlyRefresh(b *testing.B) {
	for _, size := range []int{128, 4096, 32768, 131072} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			m := activeSectionBenchmarkModel(b, size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				next, _ := m.Update(m.spinner.Tick())
				m = next.(Model)
				next, _ = m.Update(streamFlushMsg{})
				m = next.(Model)
				activeSectionBenchmarkSink = m.viewport.View()
			}
		})
	}
}

// Alternate complete source snapshots so every call must render newly received
// text. This exposes cache-key overhead rather than benchmarking only hits.
func BenchmarkTUIActiveSectionCacheMiss(b *testing.B) {
	for _, size := range []int{128, 4096, 32768} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			m := activeSectionBenchmarkModel(b, size)
			base := m.current
			tails := [2]string{base + " tail-A", base + " tail-B"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.current = tails[i%2]
				m.refreshTranscript()
				activeSectionBenchmarkSink = m.viewport.View()
			}
		})
	}
}
