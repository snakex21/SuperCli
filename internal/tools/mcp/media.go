package mcp

import (
	"encoding/json"
	"strings"

	"supercli/internal/tools/core"
)

// Image visibility modes for MCP image results. They are SuperCli-only
// extensions; plain MCP servers never see them. A server may hint a mode per
// tool through tool `_meta["supercli/image_mode"]` (ignored by other clients).
const (
	ImageModeModel   = "model"   // pixels to the model + user preview + path note
	ImageModePreview = "preview" // user preview only; the model gets a path line
	ImageModeFile    = "file"    // saved to session media only; path line
)

// MetaImageModeKey is the tool `_meta` key carrying a per-tool mode hint.
const MetaImageModeKey = "supercli/image_mode"

// NormalizeImageMode returns a valid mode or "" for unknown/empty input.
func NormalizeImageMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ImageModeModel:
		return ImageModeModel
	case ImageModePreview:
		return ImageModePreview
	case ImageModeFile:
		return ImageModeFile
	}
	return ""
}

// ResolveImageMode applies the documented precedence:
// per-server config > tool hint > global > default "model".
func ResolveImageMode(server, toolHint, global string) string {
	for _, candidate := range []string{server, toolHint, global} {
		if mode := NormalizeImageMode(candidate); mode != "" {
			return mode
		}
	}
	return ImageModeModel
}

// toolImageModeHint reads the optional `_meta["supercli/image_mode"]` hint.
func toolImageModeHint(td ToolDef) string {
	if len(td.Meta) == 0 {
		return ""
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(td.Meta, &meta) != nil {
		return ""
	}
	var mode string
	if raw, ok := meta[MetaImageModeKey]; !ok || json.Unmarshal(raw, &mode) != nil {
		return ""
	}
	return NormalizeImageMode(mode)
}

// imageModeFor resolves the mode of one tool of server s under global.
func (s *Server) imageModeFor(toolName, global string) string {
	real, declared, _ := s.toolDef(toolName)
	hint := toolImageModeHint(real)
	if hint == "" {
		hint = toolImageModeHint(declared)
	}
	return ResolveImageMode(s.Config.ImageMode, hint, global)
}

// shapeResult converts a successful MCP result into a tool result. Text over
// the head/tail budget keeps a preview while the full text is retained by
// the output store (the model gets a read_output handle instead of losing the
// middle). Images are routed by mode; binary resources are always files.
func shapeResult(res Result, mode string, head, tail int) core.Result {
	out := core.Result{Text: core.HeadTail(res.Text, head, tail)}
	if out.Text != res.Text {
		out.RetainedText = res.Text
	}
	switch NormalizeImageMode(mode) {
	case ImageModePreview:
		out.PreviewImages = res.Images
	case ImageModeFile:
		out.Files = append(out.Files, res.Images...)
	default:
		out.Images = res.Images
		out.MediaNote = len(res.Images) > 0
	}
	out.Files = append(out.Files, res.Files...)
	return out
}
