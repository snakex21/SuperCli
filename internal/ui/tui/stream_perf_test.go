package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/agent"
)

func BenchmarkTUIUnchangedStreamFrame(b *testing.B) {
	for _, size := range []int{4096, 32768} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			m := New(Options{Home: b.TempDir(), NoColor: true})
			out, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
			m = out.(Model)
			m.busy = true
			m.eventCh = make(chan agent.Event)
			line := "A result with **bold** and inline code, ready for inspection.\n"
			m.current = strings.Repeat(line, size/len(line)+1)[:size]
			m.refreshTranscript()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, _ := m.Update(streamFlushMsg{})
				m = out.(Model)
			}
		})
	}
}

func BenchmarkTUIStreamEvents(b *testing.B) {
	m0 := New(Options{Home: b.TempDir(), NoColor: true})
	out, _ := m0.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
	m0 = out.(Model)
	chunks := []string{"Result: ", "**ready** ", "for testing.\n"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := m0
		m.busy = true
		m.eventCh = make(chan agent.Event)
		for n := 0; n < 180; n++ {
			out, _ := m.Update(runEventMsg{ev: agent.MessageEvent{Text: chunks[n%len(chunks)]}})
			m = out.(Model)
			// One extra frame with no new model text, as with interleaved UI ticks.
			out, _ = m.Update(streamFlushMsg{})
			m = out.(Model)
		}
		if len(m.current) == 0 {
			b.Fatal("lost stream")
		}
	}
}
