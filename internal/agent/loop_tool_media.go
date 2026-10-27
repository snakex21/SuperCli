package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// externalizeToolMedia saves bytes to the session media store (the same store
// used for screenshots and read_image). ok=false means no durable store.
func (l *Loop) externalizeToolMedia(ctx context.Context, media *tools.ImageContent) (llm.ImageRef, bool) {
	if media == nil || len(media.Data) == 0 {
		return llm.ImageRef{}, false
	}
	ext, ok := l.writer.(imageExternalizer)
	if !ok && l.imageStore != nil {
		ext, ok = l.imageStore, true
	}
	if !ok {
		return llm.ImageRef{}, false
	}
	ref, err := ext.ExternalizeImage(ctx, media.MediaType, media.Data)
	if err != nil || ref.Path == "" {
		return llm.ImageRef{}, false
	}
	return ref, true
}

// toolMediaNote is the model-facing text line for one saved media item.
func toolMediaNote(kind, path string, media *tools.ImageContent, detail string) string {
	label := ""
	if media.Name != "" {
		label = " " + media.Name
	}
	if path == "" {
		return fmt.Sprintf("[%s%s (%s, %d bytes) not saved: no session media store]", kind, label, media.MediaType, len(media.Data))
	}
	return fmt.Sprintf("[%s%s saved: %s (%s, %d bytes)%s]", kind, label, path, media.MediaType, len(media.Data), detail)
}

// toolPreviewAndFileMedia persists user-only preview images and plain files.
// Preview images join the tool card thumbnails (previews); the model only
// receives the returned text lines, never pixels.
func (l *Loop) toolPreviewAndFileMedia(ctx context.Context, toolName string, res tools.Result, previews *[]llm.ImageRef) []string {
	var notes []string
	for _, image := range res.PreviewImages {
		if image == nil || len(image.Data) == 0 {
			continue
		}
		ref, ok := l.externalizeToolMedia(ctx, image)
		if !ok {
			notes = append(notes, toolMediaNote("image", "", image, ""))
			continue
		}
		if ref.ID != "" {
			*previews = append(*previews, llm.ImageRef{Path: ref.Path, ID: ref.ID, MediaType: ref.MediaType, Name: "tool " + toolName})
		}
		notes = append(notes, toolMediaNote("image", ref.Path, image, "; preview shown to the user, not sent to the model"))
	}
	for _, file := range res.Files {
		if file == nil || len(file.Data) == 0 {
			continue
		}
		ref, ok := l.externalizeToolMedia(ctx, file)
		if !ok {
			notes = append(notes, toolMediaNote("file", "", file, ""))
			continue
		}
		notes = append(notes, toolMediaNote("file", ref.Path, file, ""))
	}
	return notes
}

// withToolProgress installs a per-call progress sink that emits
// ToolProgressEvent without ever blocking the tool (events may be dropped
// under back-pressure) and never after the returned stop function ran.
func withToolProgress(ctx context.Context, callID string, out chan<- Event) (context.Context, func()) {
	if out == nil {
		return ctx, func() {}
	}
	var mu sync.Mutex
	done := false
	sink := func(p tools.ToolProgress) {
		mu.Lock()
		defer mu.Unlock()
		if done {
			return
		}
		select {
		case out <- ToolProgressEvent{ID: callID, Progress: p.Progress, Total: p.Total, Message: p.Message, Text: tools.FormatToolProgress(p)}:
		default:
		}
	}
	return tools.WithToolProgress(ctx, sink), func() {
		mu.Lock()
		done = true
		mu.Unlock()
	}
}

// previewPaths returns durable media paths of user-only preview images so
// they can be saved with the tool result record (transcript rebuild).
func previewPaths(refs []llm.ImageRef) []string {
	var out []string
	for _, ref := range refs {
		if ref.Path != "" {
			out = append(out, ref.Path)
		}
	}
	return out
}

func appendMediaNotes(content string, notes []string) string {
	if len(notes) == 0 {
		return content
	}
	joined := strings.Join(notes, "\n")
	if content == "" {
		return joined
	}
	return content + "\n" + joined
}
