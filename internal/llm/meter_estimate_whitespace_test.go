package llm

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestNonWhitespaceClassifierPreservesByteCounts(t *testing.T) {
	check := func(raw []byte) {
		t.Helper()
		before := bytes.Clone(raw)
		text := string(raw)
		want := referenceNonWhitespaceBytes(raw)
		if got := nonWhitespaceLen(text); got != want {
			t.Fatalf("string length=%d: count=%d want=%d", len(raw), got, want)
		}
		if got := nonWhitespaceBytes(raw); got != want {
			t.Fatalf("bytes length=%d: count=%d want=%d", len(raw), got, want)
		}
		if !bytes.Equal(raw, before) {
			t.Fatal("counter changed input bytes")
		}
	}
	// Every possible byte, including controls and invalid UTF-8, on both sides
	// of the unchanged SIMD threshold and around shorter word boundaries.
	for _, length := range []int{0, 1, 7, 8, 15, 16, 31, 32, 47, 48, 63, 64, 65, 127, 128, 129, 511, 512, 8192} {
		for value := 0; value < 256; value++ {
			check(bytes.Repeat([]byte{byte(value)}, length))
		}
		for _, kind := range whitespaceCountCorpora {
			check([]byte(whitespaceCountText(kind, length)))
		}
	}
	random := rand.New(rand.NewSource(1))
	for iteration := 0; iteration < 3000; iteration++ {
		raw := make([]byte, random.Intn(8193))
		_, _ = random.Read(raw)
		check(raw)
	}
}

func TestNonWhitespaceClassifierDoesNotAllocate(t *testing.T) {
	for _, kind := range whitespaceCountCorpora {
		for _, length := range []int{0, 31, 63, 64, 8192} {
			t.Run(fmt.Sprintf("%s/bytes=%d", kind, length), func(t *testing.T) {
				text := whitespaceCountText(kind, length)
				raw := []byte(text)
				want := referenceNonWhitespaceBytes(raw)
				if got := testing.AllocsPerRun(100, func() { nonWhitespaceCountSink = nonWhitespaceLen(text) }); got != 0 {
					t.Fatalf("string allocations=%g", got)
				}
				if nonWhitespaceCountSink != want {
					t.Fatal("string count changed")
				}
				if got := testing.AllocsPerRun(100, func() { nonWhitespaceCountSink = nonWhitespaceBytes(raw) }); got != 0 {
					t.Fatalf("bytes allocations=%g", got)
				}
				if nonWhitespaceCountSink != want {
					t.Fatal("byte count changed")
				}
			})
		}
	}
}

var whitespaceCountCorpora = []string{"english", "polish", "multilingual", "code", "whitespace", "binary"}

func whitespaceCountText(kind string, size int) string {
	var seed string
	switch kind {
	case "english":
		seed = "The agent inspected the file and reused exact evidence before changing the code.\r\n"
	case "polish":
		seed = "Zażółć gęślą jaźń. Agent sprawdził plik i zachował historię, żeby nie ponawiać odczytu.\r\n"
	case "multilingual":
		seed = "日本語の履歴を保持 東京 Ελληνικά العربية हिन्दी русский 😀 café\u00a0\u2003 retained bytes.\r\n"
	case "code":
		seed = "\t\tif result.Err != nil {\r\n\t\t\treturn fmt.Errorf(\"read failed: %w\", result.Err)\r\n\t\t}\r\n"
	case "whitespace":
		seed = " \t\n\r \t \r\n"
	case "binary":
		raw := make([]byte, 256)
		for i := range raw {
			raw[i] = byte(i)
		}
		seed = string(raw)
	}
	return strings.Repeat(seed, (size+len(seed)-1)/len(seed))[:size]
}

var nonWhitespaceCountSink int

func BenchmarkNonWhitespaceCount(b *testing.B) {
	for _, kind := range whitespaceCountCorpora {
		for _, size := range []int{15, 31, 63, 64, 128, 512, 8192} {
			text := whitespaceCountText(kind, size)
			raw := []byte(text)
			b.Run(fmt.Sprintf("%s/bytes=%d/string", kind, size), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(size))
				for b.Loop() {
					nonWhitespaceCountSink = nonWhitespaceLen(text)
				}
			})
			b.Run(fmt.Sprintf("%s/bytes=%d/raw", kind, size), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(size))
				for b.Loop() {
					nonWhitespaceCountSink = nonWhitespaceBytes(raw)
				}
			})
		}
	}
}
