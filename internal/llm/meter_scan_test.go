package llm

import (
	"fmt"
	"strings"
	"testing"
)

// Retained reference fixes the byte-based estimator's semantics, including
// Unicode whitespace and malformed UTF-8 (only four ASCII bytes are removed).
func scalarNonWhitespaceLen(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
		default:
			n++
		}
	}
	return n
}

func TestNonWhitespaceByteParity(t *testing.T) {
	samples := []string{"", "x", " \t\r\n", "\v\f\u00a0\u2003", "Zażółć gęślą jaźń 中文 😀\n", string([]byte{0, 0xff, 0xc0, ' ', 0x80, '\n'})}
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	samples = append(samples, string(all), strings.Repeat(string(all), 64))
	for _, base := range samples {
		for prefix := 0; prefix < 33; prefix++ {
			s := strings.Repeat("x", prefix) + base
			ends := []int{len(s)}
			for end := 0; end <= min(len(s), 256); end++ {
				ends = append(ends, end)
			}
			for end := 257; end < len(s); end += 257 {
				ends = append(ends, end)
			}
			for _, end := range ends {
				if got, want := nonWhitespaceLen(s[:end]), scalarNonWhitespaceLen(s[:end]); got != want {
					t.Fatalf("length=%d sample=%q got=%d want=%d", end, s[:end], got, want)
				}
			}
		}
	}
}

func BenchmarkNonWhitespaceScan(b *testing.B) {
	for _, length := range []int{0, 8, 32, 128, 4096, 32768} {
		text := strings.Repeat("\tif err != nil { return fmt.Errorf(\"odczyt: %w\", err) }\r\n", length/50+1)[:length]
		want := scalarNonWhitespaceLen(text)
		b.Run(fmt.Sprint(length), func(b *testing.B) {
			for _, method := range []string{"scalar", "count"} {
				b.Run(method, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(text)))
					for b.Loop() {
						got := 0
						if method == "scalar" {
							got = scalarNonWhitespaceLen(text)
						} else {
							got = len(text) - strings.Count(text, " ") - strings.Count(text, "\t") - strings.Count(text, "\n") - strings.Count(text, "\r")
						}
						if got != want {
							b.Fatal(got, want)
						}
					}
				})
			}
		})
	}
}
