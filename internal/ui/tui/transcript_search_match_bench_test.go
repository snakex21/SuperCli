package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

var transcriptSearchColdBenchmarkResult []transcriptMatch

func transcriptSearchColdSyntheticHistory(kind string) ([]msg, int64) {
	messages := make([]msg, 0, 1703)
	payload := strings.Repeat("Generated field ALPHA \t beta.\n", 100)
	switch kind {
	case "unicode":
		payload = strings.Repeat("Zażółć GĘŚLĄ\tΚΌΣΜΟΣ 日本語 😀\u2003 ALPHA.\n", 100)
	case "invalid_utf8":
		payload = strings.Repeat("Raw\xffA ALPHA\tfield.\n", 160)
	}
	var bytes int64
	for i := 0; i < 1703; i++ {
		tail := "ordinary"
		if i%17 == 0 {
			tail = "NEEDLE"
		}
		prefix := fmt.Sprintf("%04d ", i)
		middle := payload[:2000-len(prefix)-len(tail)]
		if kind == "unicode" {
			for !utf8.ValidString(middle) {
				middle = middle[:len(middle)-1]
			}
		}
		text := prefix + middle + tail
		messages = append(messages, msg{role: role(i % 4), text: text})
		bytes += int64(len(text))
	}
	return messages, bytes
}

func benchmarkTranscriptSearchColdVariants(b *testing.B, messages []msg, bytes int64, query string) {
	want := transcriptSearchColdReference(messages, query)
	for _, variant := range []string{"reference", "cold", "warm"} {
		b.Run(variant, func(b *testing.B) {
			c := chat{msgs: messages}
			if got := c.search(query); !reflect.DeepEqual(got, want) {
				b.Fatal("benchmark fixture differs from the original cold path")
			}
			if variant != "warm" {
				b.SetBytes(bytes)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				switch variant {
				case "reference":
					transcriptSearchColdBenchmarkResult = transcriptSearchColdReference(messages, query)
				case "cold":
					c.invalidateTranscriptSearch()
					transcriptSearchColdBenchmarkResult = c.search(query)
				case "warm":
					transcriptSearchColdBenchmarkResult = c.search(query)
				}
			}
		})
	}
}

func BenchmarkTranscriptSearchCold1703(b *testing.B) {
	for _, kind := range []string{"ascii", "unicode", "invalid_utf8"} {
		messages, bytes := transcriptSearchColdSyntheticHistory(kind)
		queries := []string{"needle", "missing", "alpha"}
		if kind == "invalid_utf8" {
			queries = []string{"needle", "missing", "�a"}
		}
		for _, query := range queries {
			b.Run(kind+"/"+query, func(b *testing.B) {
				benchmarkTranscriptSearchColdVariants(b, messages, bytes, query)
			})
		}
	}
}

func BenchmarkTranscriptSearchColdOverlap(b *testing.B) {
	messages := make([]msg, 1703)
	text := strings.Repeat("a", 1999) + "b"
	for i := range messages {
		messages[i] = msg{role: role(i % 4), text: text}
	}
	for _, queryBytes := range []int{16, 32, 64, 65, 97} {
		b.Run(fmt.Sprintf("bytes_%d", queryBytes), func(b *testing.B) {
			benchmarkTranscriptSearchColdVariants(b, messages, 1703*2000, strings.Repeat("a", queryBytes-1)+"c")
		})
	}
}
