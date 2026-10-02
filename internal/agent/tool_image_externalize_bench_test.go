package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

type toolImageBenchWriter struct{}

func (toolImageBenchWriter) AppendMessage(context.Context, llm.Message) error { return nil }
func (toolImageBenchWriter) UpdateUsage(int, int) error                       { return nil }
func (toolImageBenchWriter) ExternalizeImage(_ context.Context, media string, _ []byte) (llm.ImageRef, error) {
	return llm.ImageRef{MediaType: media, Path: "session-media/benchmark.png", ID: "img_benchmark"}, nil
}

var toolImageBenchmarkSink *llm.ImageRef

// BenchmarkToolImageExternalize exercises the actual successful tool dispatch,
// including the optional durable writer and the user image follow-up.
func BenchmarkToolImageExternalize(b *testing.B) {
	for _, size := range []int{128, 1 << 20, 10 << 20} {
		for _, durable := range []bool{false, true} {
			b.Run(fmt.Sprintf("%dB/durable=%t", size, durable), func(b *testing.B) {
				raw := make([]byte, size)
				for i := range raw {
					raw[i] = byte(i)
				}
				reg := tools.NewRegistry()
				reg.MustRegister(tools.Tool{
					Name: "fixture_image", Description: "benchmark image", Schema: "{}", ReadOnly: true,
					Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
						return tools.Result{Text: "captured", Image: &tools.ImageContent{MediaType: "image/png", Data: raw}}, nil
					},
				})
				var writer SessionWriter
				if durable {
					writer = toolImageBenchWriter{}
				}
				loop, err := NewLoop(LoopConfig{
					Provider: &stubProvider{name: "fixture"}, Registry: reg, Writer: writer, MaxSteps: 1,
				})
				if err != nil {
					b.Fatal(err)
				}
				events := make(chan Event, 8)
				invoke := func() {
					result := loop.invoke(context.Background(), llm.ToolCall{ID: "image1", Name: "fixture_image", Arguments: "{}"}, events)
					if result.failed || len(result.followUps) != 2 || len(result.followUps[1].Parts) != 2 {
						b.Fatalf("image result: %+v", result)
					}
					toolImageBenchmarkSink = result.followUps[1].Parts[1].Image
					for len(events) > 0 {
						<-events
					}
				}
				invoke() // Install load_session_image and warm registry metadata.
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					invoke()
				}
			})
		}
	}
}
