package agent

import (
	"context"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Preview-mode images are recorded with the tool result (transcript only) so
// a reopened session can rebuild the user-only thumbnails.
func TestToolPreviewPathsPersistWithToolResult(t *testing.T) {
	writer := &toolImageFixtureWriter{ref: llm.ImageRef{MediaType: "image/png", Path: "session-media/preview.png", ID: "img_preview"}}
	png := &tools.ImageContent{MediaType: "image/png", Data: []byte("pixels")}
	result, _ := toolImageFixtureInvoke(t, writer, tools.Result{Text: "done", PreviewImages: []*tools.ImageContent{png}}, nil)
	origin := result.followUps[0].Origin
	if origin == nil || len(origin.ToolPreviews) != 1 || origin.ToolPreviews[0] != "session-media/preview.png" {
		t.Fatalf("preview paths not recorded: %+v", origin)
	}
	rec := &previewRecordingWriter{}
	loop := &Loop{writer: rec}
	var batch toolBatchMessages
	batch.append(loop, context.Background(), result.followUps)
	if len(rec.msgs) != 1 || rec.msgs[0].Origin == nil || rec.msgs[0].Origin.Source != llm.SourceToolResult ||
		len(rec.msgs[0].Origin.ToolPreviews) != 1 {
		t.Fatalf("persisted origin = %+v", rec.msgs)
	}
	if got := loop.Messages[0].Origin; got == nil || len(got.ToolPreviews) != 0 {
		t.Fatalf("model history must not carry preview paths: %+v", got)
	}
	// Model-mode images keep no extra record (their carrier already exists).
	result, _ = toolImageFixtureInvoke(t, writer, tools.Result{Text: "x", Images: []*tools.ImageContent{png}}, nil)
	if result.followUps[0].Origin != nil {
		t.Fatalf("model image added preview record: %+v", result.followUps[0].Origin)
	}
}

type previewRecordingWriter struct{ msgs []llm.Message }

func (w *previewRecordingWriter) AppendMessage(_ context.Context, m llm.Message) error {
	w.msgs = append(w.msgs, m)
	return nil
}
func (*previewRecordingWriter) UpdateUsage(int, int) error { return nil }

func TestToolProgressSinkEmitsAndStops(t *testing.T) {
	out := make(chan Event, 4)
	ctx, stop := withToolProgress(context.Background(), "call-1", out)
	if !tools.ReportToolProgress(ctx, tools.ToolProgress{Progress: 1, Total: 4, Message: "a"}) {
		t.Fatal("sink not installed")
	}
	ev := (<-out).(ToolProgressEvent)
	if ev.ID != "call-1" || ev.Text != "25% · a" {
		t.Fatalf("event = %+v", ev)
	}
	stop()
	tools.ReportToolProgress(ctx, tools.ToolProgress{Progress: 2})
	if len(out) != 0 {
		t.Fatal("progress emitted after the call finished")
	}
}
