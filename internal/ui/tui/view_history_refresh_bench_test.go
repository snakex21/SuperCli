package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"testing"
)

func BenchmarkTUIHistoryRefreshAppend(b *testing.B) {
	for _, count := range []int{60, 600, 2400} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			base := New(Options{Home: b.TempDir(), NoColor: true})
			out, _ := base.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
			base = out.(Model)
			base.chat = completedHistoryBenchmarkFixture(count)
			base.refreshTranscript()
			_ = base.View()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				m := base
				m.chat.msgs = append([]msg(nil), base.chat.msgs...)
				b.StartTimer()
				m.chat.addToolResult("read_lines", "new generated row\nsecond row", "")
				m.refreshTranscript()
				_ = m.View()
			}
		})
	}
}
