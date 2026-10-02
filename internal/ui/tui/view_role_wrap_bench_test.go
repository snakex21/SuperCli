package tui

import (
	"fmt"
	"strings"
	"testing"
)

func completedHistoryBenchmarkFixture(count int) chat {
	c := newChat(100, "en")
	c.thinkingCollapsed = true
	c.legacySymbols = false
	for i := 0; i < count; i++ {
		switch {
		case i%80 == 0:
			c.addUser("Please inspect the generated project. " + strings.Repeat("bounded request ", 22))
		case i%2 == 1:
			c.addToolResult("read_lines", strings.Repeat("line | const value = fixture; generated read output.\n", 44), "")
		case i%8 == 0:
			c.addSystem("tool_call read_lines src/generated.go")
		default:
			c.addAssistant(strings.Repeat("Generated **code** explanation and ordinary project information. ", 31))
		}
	}
	return c
}
func BenchmarkTUICompletedHistoryAppend(b *testing.B) {
	p := NoColorPalette()
	for _, count := range []int{60, 600, 2400} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			base := completedHistoryBenchmarkFixture(count)
			_ = base.renderCompleted(p)
			b.Run("CompletedAppend", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					c := base
					c.msgs = append([]msg(nil), base.msgs...)
					b.StartTimer()
					c.addToolResult("read_lines", "new generated row\nsecond row", "")
					_ = c.renderCompleted(p)
				}
			})
			b.Run("Unchanged", func(b *testing.B) {
				c := base
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = c.renderCompleted(p)
				}
			})
		})
	}
}
