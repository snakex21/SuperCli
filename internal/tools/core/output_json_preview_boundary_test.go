package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPreviewJSONStringBoundaryCompatibility(t *testing.T) {
	values := []string{
		"", "plain short text", "zażółć gęślą jaźń Ελληνικά 日本語 😀",
		"quote \" slash \\ controls \n\r\t\b\f <>&",
		strings.Repeat("\\", 257) + "u1234 \" unicode 😀 end",
		strings.Repeat("line with a path C:\\repo\\code.go and unicode 🙂\n", 25),
	}
	raws := []json.RawMessage{
		json.RawMessage("\"\\u0041\\u00ff\\ud83d\\ude00\\\\u0041\\\"end\""),
	}
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		raws = append(raws, raw)
	}
	for i, raw := range raws {
		for budget := 2; budget <= len(raw)+4; budget++ {
			got, cut := PreviewJSONString(raw, budget)
			want, wantCut := previewJSONStringLegacyBoundary(raw, budget)
			if cut != wantCut || !bytes.Equal(got, want) {
				t.Fatalf("case=%d budget=%d got=%q cut=%v want=%q cut=%v", i, budget, got, cut, want, wantCut)
			}
			if !json.Valid(got) || !utf8.Valid(got) || len(got) > budget {
				t.Fatalf("invalid/big preview case=%d budget=%d: %q", i, budget, got)
			}
		}
	}
}

func TestPreviewJSONStringBoundaryRandomCompatibility(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	alphabet := []rune("abcdABC0123\\\"/\n\r\t\b\f<>&żλ日本😀")
	for iteration := 0; iteration < 300; iteration++ {
		runes := make([]rune, 1+rng.Intn(600))
		for i := range runes {
			runes[i] = alphabet[rng.Intn(len(alphabet))]
		}
		raw, err := json.Marshal(string(runes))
		if err != nil {
			t.Fatal(err)
		}
		for sample := 0; sample < 20; sample++ {
			budget := 2 + rng.Intn(len(raw)+20)
			got, cut := PreviewJSONString(raw, budget)
			want, wantCut := previewJSONStringLegacyBoundary(raw, budget)
			if cut != wantCut || !bytes.Equal(got, want) || !json.Valid(got) || !utf8.Valid(got) {
				t.Fatalf("iteration=%d budget=%d got=%q want=%q", iteration, budget, got, want)
			}
		}
	}
}

func TestPreviewJSONStringBoundaryLongSlashRun(t *testing.T) {
	raw, _ := json.Marshal(strings.Repeat("\\", 32768) + "u0041 end")
	for _, budget := range []int{2, 36, 37, 38, 40, 127, 4096, 4097} {
		got, cut := PreviewJSONString(raw, budget)
		want, wantCut := previewJSONStringLegacyBoundary(raw, budget)
		if cut != wantCut || !bytes.Equal(got, want) || !json.Valid(got) {
			t.Fatalf("budget=%d got=%q want=%q", budget, got, want)
		}
	}
}

var previewJSONStringBoundarySink json.RawMessage

func BenchmarkPreviewJSONStringBoundary(b *testing.B) {
	for _, size := range []int{256, 4096, 65536, 2 * 1024 * 1024} {
		for _, kind := range []string{"ascii", "escaped", "unicode"} {
			unit := "log output value ok\n"
			if kind == "escaped" {
				unit = "C:\\repo\\file.go \"quote\" <>&\n"
			}
			if kind == "unicode" {
				unit = "zażółć 日本語 😀\n"
			}
			value := strings.Repeat(unit, (size+len(unit)-1)/len(unit))
			raw, err := json.Marshal(value)
			if err != nil {
				b.Fatal(err)
			}
			b.Run(fmt.Sprintf("%s/%d", kind, size), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					previewJSONStringBoundarySink, _ = PreviewJSONString(raw, 4096)
				}
			})
		}
	}
}

// Compatibility oracle is the previous public output contract.
func previewJSONStringLegacyBoundary(encoded json.RawMessage, budget int) (json.RawMessage, bool) {
	if len(encoded) <= budget {
		return encoded, false
	}
	const omitted = `\n[... output omitted from preview ...]\n`
	keep := budget - len(omitted) - 2
	if keep <= 0 {
		return json.RawMessage(`""`), true
	}
	payload := encoded[1 : len(encoded)-1]
	headLimit := keep * 3 / 4
	tailLimit := len(payload) - (keep - headLimit)
	head, tail := 0, 0
	for i := 0; i < len(payload); {
		next := i + 1
		switch {
		case payload[i] == '\\':
			next = i + 2
			if payload[i+1] == 'u' {
				next = i + 6
			}
		case payload[i] >= utf8.RuneSelf:
			_, size := utf8.DecodeRune(payload[i:])
			next = i + size
		}
		if next <= headLimit {
			head = next
		}
		if next >= tailLimit {
			tail = next
			break
		}
		i = next
	}
	result := make([]byte, 0, budget)
	result = append(result, '"')
	result = append(result, payload[:head]...)
	result = append(result, omitted...)
	result = append(result, payload[tail:]...)
	result = append(result, '"')
	return result, true
}

func BenchmarkPreviewJSONStringBoundaryPair(b *testing.B) {
	for _, fixture := range []struct {
		name, unit string
		size       int
	}{
		{"small", "plain", 256},
		{"small-cut", "plain log line\n", 4096},
		{"large-ascii", "plain log line\n", 2 * 1024 * 1024},
		{"large-unicode", "zażółć 日本語 😀\n", 2 * 1024 * 1024},
	} {
		raw, _ := json.Marshal(strings.Repeat(fixture.unit, (fixture.size+len(fixture.unit)-1)/len(fixture.unit)))
		for _, version := range []struct {
			name string
			fn   func(json.RawMessage, int) (json.RawMessage, bool)
		}{
			{"before", previewJSONStringLegacyBoundary}, {"after", PreviewJSONString},
		} {
			b.Run(fixture.name+"/"+version.name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					previewJSONStringBoundarySink, _ = version.fn(raw, 4096)
				}
			})
		}
	}
}
