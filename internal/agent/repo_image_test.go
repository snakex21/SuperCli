package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestRepoImageReachesModelWithoutUserAttachment(t *testing.T) {
	for _, thin := range []bool{false, true} {
		for _, worker := range []bool{false, true} {
			t.Run(fmt.Sprintf("thin=%v/worker=%v", thin, worker), func(t *testing.T) {
				dir := t.TempDir()
				const name = "zrzut_żółć.png"
				img := image.NewRGBA(image.Rect(0, 0, 2, 2))
				img.Set(0, 0, color.RGBA{R: 255, A: 255})
				var encoded bytes.Buffer
				if err := png.Encode(&encoded, img); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), encoded.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
				args, _ := json.Marshal(map[string]string{"path": name})
				p := &stubProvider{name: "vision-model", scripts: [][]llm.Delta{
					{{ToolCall: &llm.ToolCall{ID: "list", Name: "list_dir", Arguments: `{"path":"."}`}}},
					{{ToolCall: &llm.ToolCall{ID: "image", Name: "read_image", Arguments: string(args)}}},
					{{Content: "Image inspected.", FinishReason: "stop"}},
				}}
				reg := tools.NewRegistry()
				reg.MustRegister(tools.NewListDir(dir).Spec())
				reg.MustRegister(tools.NewReadImage(dir, 0).Spec())
				reg.MarkAlwaysOn("list_dir")
				reg.MarkAlwaysOn("read_image")
				loop, err := NewLoop(LoopConfig{Provider: p, Registry: reg, BaseDir: dir, ThinTools: thin, StableToolset: true, MaxSteps: 5})
				if err != nil {
					t.Fatal(err)
				}
				var task *AgentTool
				if worker {
					specs := NewSubAgentRegistry()
					MustRegisterAll(specs, BuiltinSubAgents())
					task, err = NewAgentTool(specs, loop, reg, p, nil, NewLoop)
					if err != nil {
						t.Fatal(err)
					}
					result, err := task.execute(context.Background(), json.RawMessage(`{"agent":"explore","prompt":"Find and inspect the image in this repository."}`))
					if err != nil || result.Err != nil {
						t.Fatalf("task: %v %+v", err, result)
					}
				} else {
					events, err := loop.Run(context.Background(), "Find and inspect the image in this repository.")
					if err != nil {
						t.Fatal(err)
					}
					drainEvents(t, events)
				}
				if p.calls != 3 {
					t.Fatalf("requests=%d, want list -> read image -> answer", p.calls)
				}
				for _, request := range p.reqs[:2] {
					for _, m := range request {
						if m.HasImage() {
							t.Fatal("image present before read_image; fixture must have no attachment")
						}
					}
				}
				listed := false
				for _, m := range p.reqs[1] {
					if m.ToolCallID == "list" && strings.Contains(m.Content, name) {
						listed = true
					}
				}
				if !listed {
					t.Fatal("repository listing did not expose image filename")
				}
				images := 0
				for _, m := range p.reqs[2] {
					for _, part := range m.Parts {
						if part.Type != llm.PartTypeImage || part.Image == nil {
							continue
						}
						images++
						got, err := base64.StdEncoding.DecodeString(part.Image.Data)
						if err != nil || !bytes.Equal(got, encoded.Bytes()) || part.Image.MediaType != "image/png" {
							t.Fatal("model did not receive exact image pixels from repository")
						}
					}
				}
				if images != 1 {
					t.Fatalf("image parts=%d, want 1", images)
				}
				if worker {
					follow := NewSendMessageTool(task.Workers)
					result, err := follow.execute(context.Background(), json.RawMessage(`{"to":"worker-1","message":"Continue with your findings."}`))
					if err != nil || result.Err != nil {
						t.Fatalf("resume: %v %+v", err, result)
					}
				} else {
					events, err := loop.Run(context.Background(), "Continue with your findings.")
					if err != nil {
						t.Fatal(err)
					}
					drainEvents(t, events)
				}
				if p.calls != 4 {
					t.Fatalf("continuation requests=%d", p.calls)
				}
				for _, m := range p.reqs[3] {
					if m.HasImage() {
						t.Fatal("image pixels resent without a new request to read them")
					}
				}
			})
		}
	}
}
