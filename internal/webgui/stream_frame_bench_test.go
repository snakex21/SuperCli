package webgui

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestSSEFrameWriterParity(t *testing.T) {
	events := []wireEvent{{Type: "message", Text: "Zażółć 中文 😀 <>&\u2028\u2029\nquote\" slash\\"}, {Type: "tool_result", Output: strings.Repeat("large \noutput ", 10000)}, {Type: "reasoning", ReasoningTok: 123}, {Type: "done", TokIn: 10, TokOut: 2}, {Type: "error", Err: "problem"}}
	for _, ev := range events {
		var legacy, direct bytes.Buffer
		fmt.Fprintf(&legacy, "data: %s\n\n", ev.marshal())
		writeSSEFrame(&direct, ev.marshal())
		if !bytes.Equal(legacy.Bytes(), direct.Bytes()) {
			t.Fatalf("different frame for %s", ev.Type)
		}
	}
}

func BenchmarkSSEFrameWriter(b *testing.B) {
	for _, size := range []int{32, 4096, 131072, 1048576} {
		for _, writer := range []string{"discard", "buffered"} {
			for _, variant := range []string{"printf", "direct"} {
				b.Run(fmt.Sprintf("%d/%s/%s", size, writer, variant), func(b *testing.B) {
					ev := wireEvent{Type: "tool_result", ID: "call", Output: strings.Repeat("x", size)}
					var w io.Writer = io.Discard
					var bw *bufio.Writer
					if writer == "buffered" {
						bw = bufio.NewWriterSize(io.Discard, 4096)
						w = bw
					}
					b.ReportAllocs()
					b.SetBytes(int64(size))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if variant == "printf" {
							fmt.Fprintf(w, "data: %s\n\n", ev.marshal())
						} else {
							writeSSEFrame(w, ev.marshal())
						}
						if bw != nil {
							_ = bw.Flush()
						}
					}
				})
			}
		}
	}
}
