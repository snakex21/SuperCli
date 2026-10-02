package agent

import (
	"fmt"
	"strings"
	"supercli/internal/llm"
	"testing"
)

func whitespacePreparationText(kind string, size int) string {
	var seed string
	switch kind {
	case "english":
		seed = "The agent inspected the file and reused exact evidence before changing the code.\r\n"
	case "polish":
		seed = "Zażółć gęślą jaźń. Agent zachował historię i ponownie użył zebranych wyników.\r\n"
	case "multilingual":
		seed = "日本語の履歴を保持 東京 Ελληνικά العربية हिन्दी русский 😀 café\u00a0\u2003 retained bytes.\r\n"
	case "binary":
		raw := make([]byte, 256)
		for i := range raw {
			raw[i] = byte(i)
		}
		seed = string(raw)
	}
	return strings.Repeat(seed, (size+len(seed)-1)/len(seed))[:size]
}
func whitespacePreparationFixture(b testing.TB, kind string, size int, projected bool) *Loop {
	l := projectionEstimateFixture(b, false, false, projected)
	for i := range l.Messages {
		m := &l.Messages[i]
		if m.Role == llm.RoleUser {
			m.Content = fmt.Sprintf("task_%04d ", i) + whitespacePreparationText(kind, min(size, 48))
		}
		if m.Role == llm.RoleAssistant {
			for j := range m.Parts {
				p := &m.Parts[j]
				if p.Type == llm.PartTypeText {
					p.Text = fmt.Sprintf("answer_%04d ", i) + whitespacePreparationText(kind, size)
				}
				if p.Type == llm.PartTypeReasoning && p.Reasoning != nil {
					p.Reasoning.Data = []byte(whitespacePreparationText(kind, size*4))
					p.Reasoning.Tokens = 0
				}
			}
		}
	}
	l.invalidateVisibleEstimate()
	l.EstimateNextRequestTokens()
	return l
}
func BenchmarkWhitespaceContextPreparation(b *testing.B) {
	for _, kind := range []string{"english", "polish", "multilingual", "binary"} {
		for _, size := range []int{48, 8192} {
			for _, mode := range []string{"warm", "cold", "projected"} {
				b.Run(fmt.Sprintf("%s/bytes=%d/%s", kind, size, mode), func(b *testing.B) {
					l := whitespacePreparationFixture(b, kind, size, mode == "projected")
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if mode == "cold" {
							l.invalidateVisibleEstimate()
						}
						l.EstimateNextRequestTokens()
						l.EstimateNextRequestTokens()
						defs := l.buildToolDefs()
						messages, tokens := l.prepareProviderMessages(true)
						preparedRequestEstimateSink = tokens + estimateRequestTokens(nil, defs)
						preparedRequestMessagesSink = messages
					}
				})
			}
		}
	}
}
