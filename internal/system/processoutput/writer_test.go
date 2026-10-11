package processoutput

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestTextDecoderKeepsUTF8AcrossEveryChunkBoundary(t *testing.T) {
	want := "18\u00a0847 zażółć gęślą jaźń 日本語 😀\n"
	for chunk := 1; chunk < 12; chunk++ {
		var out bytes.Buffer
		writer := NewWriter(&out)
		writer.decodeLegacy = func(p []byte) string { t.Error("valid split UTF-8 reached legacy decoder"); return string(p) }
		for i := 0; i < len(want); i += chunk {
			end := min(len(want), i+chunk)
			if n, err := writer.Write([]byte(want[i:end])); err != nil || n != end-i {
				t.Fatalf("write: %d %v", n, err)
			}
		}
		if err := writer.Close(); err != nil || out.String() != want {
			t.Fatalf("chunk=%d got=%q err=%v", chunk, out.String(), err)
		}
	}
}

func TestTextDecoderUTF16BOMAndSplitSurrogate(t *testing.T) {
	want := "18\u00a0847 zażółć 😀\n"
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		units := append([]uint16{0xfeff}, utf16.Encode([]rune(want))...)
		raw := make([]byte, 2*len(units))
		for i, unit := range units {
			order.PutUint16(raw[2*i:], unit)
		}
		for chunk := 1; chunk < 7; chunk++ {
			var out bytes.Buffer
			writer := NewWriter(&out)
			for i := 0; i < len(raw); i += chunk {
				_, _ = writer.Write(raw[i:min(len(raw), i+chunk)])
			}
			_ = writer.Close()
			if out.String() != want {
				t.Fatalf("UTF16 chunk=%d got=%q", chunk, out.String())
			}
		}
	}
}

func TestTextDecoderBOMAndIncompleteFinalOutput(t *testing.T) {
	for _, raw := range [][]byte{append([]byte{0xef, 0xbb, 0xbf}, []byte("polski\n")...), []byte("polski\n")} {
		var out bytes.Buffer
		writer := NewWriter(&out)
		for _, b := range raw {
			_, _ = writer.Write([]byte{b})
		}
		_ = writer.Close()
		if out.String() != "polski\n" {
			t.Fatalf("BOM: %q", out.String())
		}
	}
	var out bytes.Buffer
	writer := NewWriter(&out)
	_, _ = writer.Write([]byte{0xe2, 0x82})
	_ = writer.Close()
	if out.Len() == 0 {
		t.Fatal("incomplete final bytes silently lost")
	}
}

func BenchmarkUTF8ProcessOutput(b *testing.B) {
	raw := []byte(strings.Repeat("zażółć gęślą jaźń\n", 1000))
	w := NewWriter(&discardWriter{})
	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	for b.Loop() {
		_, _ = w.Write(raw)
	}
}

type discardWriter struct{}

func (*discardWriter) Write(p []byte) (int, error) { return len(p), nil }
