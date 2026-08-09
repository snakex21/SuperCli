package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

const sessionImageToolName = "load_session_image"

// ensureSessionImageTool installs a generic tool descriptor. Execution is
// intercepted by Loop.invoke so a shared Registry never captures a pointer to
// the wrong Loop/session.
func (l *Loop) ensureSessionImageTool() {
	if l == nil || l.registry == nil {
		return
	}
	if _, ok := l.registry.Get(sessionImageToolName); !ok {
		err := l.registry.Register(tools.Tool{
			Name:        sessionImageToolName,
			Description: "Load pixels for an image already attached to this conversation by its session image id.",
			ReadOnly:    true,
			Schema: `{
				"type":"object",
				"properties":{"id":{"type":"string","description":"Session image id shown in conversation history, for example img_a1b2c3d4e5f6."}},
				"required":["id"]
			}`,
			Fn: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Err: fmt.Errorf("%s: internal loop dispatcher unavailable", sessionImageToolName)}, nil
			},
		})
		if err != nil {
			// A shared registry may race another Loop registering the same
			// generic descriptor. If it exists now, the desired state won.
			if _, ok := l.registry.Get(sessionImageToolName); !ok {
				return
			}
		}
	}
}

func (l *Loop) enableSessionImageTool() {
	if l == nil || l.registry == nil {
		return
	}
	l.ensureSessionImageTool()
	if _, ok := l.registry.Get(sessionImageToolName); ok {
		l.registry.Activate(sessionImageToolName)
	}
}

func (l *Loop) hasSessionImages() bool {
	if l == nil {
		return false
	}
	for _, msg := range l.Messages {
		for _, part := range msg.Parts {
			if part.Type == llm.PartTypeImage && part.Image != nil && part.Image.ID != "" {
				return true
			}
		}
	}
	return false
}

// prepareSessionImages externalizes direct user attachments before they enter
// live history. Active=true is deliberately ephemeral: pixels are eligible for
// the next provider call only. The persisted copy is forced dormant elsewhere.
func (l *Loop) prepareSessionImages(ctx context.Context, images []llm.ImageRef) []llm.ImageRef {
	out := make([]llm.ImageRef, 0, len(images))
	for i := range images {
		img := images[i]
		if img.Path == "" && img.Data != "" {
			if ext, ok := l.writer.(imageExternalizer); ok {
				if raw, err := base64.StdEncoding.DecodeString(img.Data); err == nil {
					if ref, err := ext.ExternalizeImage(ctx, img.MediaType, raw); err == nil {
						ref.Name = img.Name
						img = ref
					}
				}
			}
		}
		img.Active = true
		out = append(out, img)
	}
	return out
}

// mediaProviderView replaces dormant image parts with tiny text handles. Active
// refs are deep-copied and remain real image parts for exactly one call.
func (l *Loop) mediaProviderView(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = msg
		if len(msg.Parts) == 0 {
			continue
		}
		out[i].Parts = make([]llm.ContentPart, 0, len(msg.Parts))
		for _, part := range msg.Parts {
			if part.Type != llm.PartTypeImage || part.Image == nil {
				out[i].Parts = append(out[i].Parts, part)
				continue
			}
			img := *part.Image
			if img.Active {
				out[i].Parts = append(out[i].Parts, llm.ContentPart{Type: llm.PartTypeImage, Image: &img})
				continue
			}
			out[i].Parts = append(out[i].Parts, llm.ContentPart{Type: llm.PartTypeText, Text: sessionImageMarker(img)})
		}
	}
	return out
}

func sessionImageMarker(img llm.ImageRef) string {
	id := strings.TrimSpace(img.ID)
	if id == "" {
		return "[previous image omitted from this request]"
	}
	name := strings.TrimSpace(img.Name)
	if name == "" && img.Path != "" {
		name = filepath.Base(img.Path)
	}
	if name != "" {
		return fmt.Sprintf("[image %s %q available via %s]", id, name, sessionImageToolName)
	}
	return fmt.Sprintf("[image %s available via %s]", id, sessionImageToolName)
}

// deactivateActiveImages is called only after Provider.Complete accepted the
// request. It mutates live history, never the request snapshot, so one image is
// never resent automatically on subsequent model calls.
func (l *Loop) deactivateActiveImages() {
	if l == nil {
		return
	}
	for mi := range l.Messages {
		for pi := range l.Messages[mi].Parts {
			part := &l.Messages[mi].Parts[pi]
			if part.Type != llm.PartTypeImage || part.Image == nil || !part.Image.Active {
				continue
			}
			part.Image.Active = false
			if part.Image.Path != "" {
				part.Image.Data = ""
			}
		}
	}
}

// loadSessionImage can only resolve ids already present in this Loop's history;
// the model cannot use it as an arbitrary filesystem reader. Reloading merely
// re-arms the existing ref for one provider call; it does not duplicate the
// image, read it twice, or append another media message to history.
func (l *Loop) loadSessionImage(ctx context.Context, raw json.RawMessage) (tools.Result, error) {
	if err := ctx.Err(); err != nil {
		return tools.Result{Err: err}, err
	}
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		err = fmt.Errorf("%s: bad args: %w", sessionImageToolName, err)
		return tools.Result{Err: err}, nil
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		err := fmt.Errorf("%s: id is required", sessionImageToolName)
		return tools.Result{Err: err}, nil
	}
	for mi := len(l.Messages) - 1; mi >= 0; mi-- {
		for pi := len(l.Messages[mi].Parts) - 1; pi >= 0; pi-- {
			part := &l.Messages[mi].Parts[pi]
			if part.Type != llm.PartTypeImage || part.Image == nil || part.Image.ID != id {
				continue
			}
			img := part.Image
			if img.Path == "" {
				err := fmt.Errorf("%s: image %q has no durable file", sessionImageToolName, id)
				return tools.Result{Err: err}, nil
			}
			info, err := os.Stat(img.Path)
			if err != nil {
				err = fmt.Errorf("%s: image %q unavailable: %w", sessionImageToolName, id, err)
				return tools.Result{Err: err}, nil
			}
			if info.Size() > 32<<20 {
				err := fmt.Errorf("%s: image %q is too large: %d bytes", sessionImageToolName, id, info.Size())
				return tools.Result{Err: err}, nil
			}
			img.Active = true
			label := img.Name
			if label == "" {
				label = filepath.Base(img.Path)
			}
			return tools.Result{Text: fmt.Sprintf("Session image %s (%s) will be attached to the next model request", id, label)}, nil
		}
	}
	err := fmt.Errorf("%s: unknown image id %q", sessionImageToolName, id)
	return tools.Result{Err: err}, nil
}
