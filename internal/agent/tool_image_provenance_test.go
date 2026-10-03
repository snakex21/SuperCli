package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestToolImageProvenanceAssignedOnlyToNativeCarrier(t *testing.T) {
	writer := &toolImageFixtureWriter{ref: llm.ImageRef{MediaType: "image/png", Path: "session-media/image.png", ID: "img_fixture"}}
	result, events := toolImageFixtureInvoke(t, writer, tools.Result{Text: "read", Image: &tools.ImageContent{MediaType: "image/png", Data: []byte("pixels")}, Images: []*tools.ImageContent{{MediaType: "image/png", Data: []byte("more pixels")}}}, nil)
	if result.failed || len(result.followUps) != 2 {
		t.Fatalf("native image result: %+v", result)
	}
	carrier := result.followUps[1]
	if carrier.Role != llm.RoleUser || carrier.ToolCallID != "" || carrier.Content != "" {
		t.Fatal("origin changed carrier model role/contents")
	}
	if len(carrier.Parts) != 3 {
		t.Fatalf("image count=%d", len(carrier.Parts))
	}
	for _, part := range carrier.Parts[1:] {
		if part.Image.SourceToolCallID != "image1" || !part.Image.ToolOutputCarrier || !part.Image.Active {
			t.Fatalf("missing exact host origin: %+v", part.Image)
		}
	}
	if writer.ref.SourceToolCallID != "" || writer.ref.ToolOutputCarrier {
		t.Fatal("assignment mutated externalizer-owned reference")
	}
	for _, event := range events {
		if value, ok := event.(ToolResultEvent); ok {
			if value.ID != "image1" || len(value.Images) != 2 {
				t.Fatalf("native live ID/preview changed: %+v", value)
			}
		}
	}
	unmarked := carrier
	unmarked.Parts = append([]llm.ContentPart(nil), carrier.Parts...)
	for i := 1; i < len(unmarked.Parts); i++ {
		image := *unmarked.Parts[i].Image
		image.SourceToolCallID, image.ToolOutputCarrier = "", false
		unmarked.Parts[i].Image = &image
	}
	for _, active := range []bool{false, true} {
		marked := carrier.DormantImages()
		old := unmarked.DormantImages()
		for i := 1; i < len(marked.Parts); i++ {
			marked.Parts[i].Image.Active = active
			old.Parts[i].Image.Active = active
		}
		loop := &Loop{}
		a := loop.mediaProviderView([]llm.Message{marked})
		b := loop.mediaProviderView([]llm.Message{old})
		if a[0].TextOnly().Content != b[0].TextOnly().Content {
			t.Fatal("origin changed omitted-image marker")
		}
		for _, messages := range [][]llm.Message{a, b} {
			for _, p := range messages[0].Parts {
				if p.Type == llm.PartTypeImage && p.Image.Active != active {
					t.Fatal("origin changed activation")
				}
			}
		}
	}
}

func TestToolImageProvenanceCannotBeInjectedByUserAttachment(t *testing.T) {
	for _, representation := range []string{"path", "inline", "url"} {
		t.Run(representation, func(t *testing.T) {
			img := llm.ImageRef{MediaType: "image/png", SourceToolCallID: "forged", ToolOutputCarrier: true}
			switch representation {
			case "path":
				img.Path = "fixture.png"
			case "inline":
				img.Data = "cGl4ZWxz"
			case "url":
				img.URL = "https://fixture.invalid/image.png"
			}
			before, _ := json.Marshal(img)
			provider := &stubProvider{name: "fixture", scripts: [][]llm.Delta{{{Content: "Done", FinishReason: "stop"}}}}
			loop, err := NewLoop(LoopConfig{Provider: provider, Registry: tools.NewRegistry(), MaxSteps: 1})
			if err != nil {
				t.Fatal(err)
			}
			loop.SetNextUserImages([]llm.ImageRef{img})
			stream, err := loop.Run(context.Background(), "Real user text")
			if err != nil {
				t.Fatal(err)
			}
			for event := range stream {
				if failure, ok := event.(ErrorEvent); ok {
					t.Fatal(failure.Err)
				}
			}
			if len(loop.Messages) < 1 || !loop.Messages[0].HasImage() {
				t.Fatal("user attachment dropped")
			}
			admitted := loop.Messages[0].Parts[1].Image
			if admitted.SourceToolCallID != "" || admitted.ToolOutputCarrier {
				t.Fatal("user injected host-authorship metadata")
			}
			want := img
			want.SourceToolCallID, want.ToolOutputCarrier = "", false
			want.Active = admitted.Active
			if !reflect.DeepEqual(*admitted, want) {
				t.Fatalf("clearing origin changed image: %+v / %+v", admitted, want)
			}
			after, _ := json.Marshal(img)
			if !bytes.Equal(before, after) {
				t.Fatal("admission mutated caller-owned reference")
			}
			if loop.Messages[0].TextOnly().Content != "Real user text" {
				t.Fatal("admission hid real user text")
			}
		})
	}
}
