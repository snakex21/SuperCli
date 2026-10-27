package agent

import (
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Preview mode: the user sees a thumbnail (ToolResultEvent.Images) but the
// model receives only a text line with the saved path; no image carrier.
func TestToolPreviewImagesNeverReachModel(t *testing.T) {
	writer := &toolImageFixtureWriter{ref: llm.ImageRef{MediaType: "image/png", Path: "session-media/preview.png", ID: "img_preview"}}
	png := &tools.ImageContent{MediaType: "image/png", Data: []byte("pixels"), Name: "mem://shot"}
	result, events := toolImageFixtureInvoke(t, writer, tools.Result{Text: "done", PreviewImages: []*tools.ImageContent{png}}, nil)
	if result.failed || len(result.followUps) != 1 {
		t.Fatalf("preview result must have no image carrier: %+v", result.followUps)
	}
	content := result.followUps[0].Content
	if !strings.Contains(content, "session-media/preview.png") || !strings.Contains(content, "not sent to the model") || !strings.Contains(content, "mem://shot") {
		t.Fatalf("model text lacks path note: %q", content)
	}
	var previewed bool
	for _, ev := range events {
		if res, ok := ev.(ToolResultEvent); ok {
			previewed = len(res.Images) == 1 && res.Images[0].Path == "session-media/preview.png" && res.Images[0].Data == ""
		}
	}
	if !previewed {
		t.Fatalf("user preview missing: %+v", events)
	}
}

func TestToolFilesAndModelImageNotes(t *testing.T) {
	writer := &toolImageFixtureWriter{ref: llm.ImageRef{MediaType: "image/png", Path: "session-media/x.bin", ID: "img_x"}}
	file := &tools.ImageContent{MediaType: "application/pdf", Data: []byte("%PDF")}
	img := &tools.ImageContent{MediaType: "image/png", Data: []byte("pixels")}
	result, events := toolImageFixtureInvoke(t, writer, tools.Result{Text: "ok", Files: []*tools.ImageContent{file}, Images: []*tools.ImageContent{img}, MediaNote: true}, nil)
	if len(result.followUps) != 2 {
		t.Fatalf("model image carrier expected: %+v", result.followUps)
	}
	content := result.followUps[0].Content
	if !strings.Contains(content, "[file saved: session-media/x.bin (application/pdf, 4 bytes)]") || !strings.Contains(content, "attached for the model") {
		t.Fatalf("notes: %q", content)
	}
	for _, ev := range events {
		if res, ok := ev.(ToolResultEvent); ok && len(res.Images) != 1 {
			t.Fatalf("files must not be previewed as thumbnails: %+v", res.Images)
		}
	}
}
