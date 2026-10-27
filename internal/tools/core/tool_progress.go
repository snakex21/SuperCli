package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ToolProgress is one live progress report from a running tool (for example
// an MCP notifications/progress message). It is UI-only: never model text.
type ToolProgress struct {
	Progress float64
	Total    float64 // zero when unknown
	Message  string
}

type toolProgressKey struct{}

// WithToolProgress attaches a per-invocation progress sink. The agent loop
// installs one per tool call; tools report through ReportToolProgress.
func WithToolProgress(ctx context.Context, fn func(ToolProgress)) context.Context {
	if ctx == nil || fn == nil {
		return ctx
	}
	return context.WithValue(ctx, toolProgressKey{}, fn)
}

// ReportToolProgress forwards p to the invocation's sink, if any.
func ReportToolProgress(ctx context.Context, p ToolProgress) bool {
	if ctx == nil {
		return false
	}
	fn, _ := ctx.Value(toolProgressKey{}).(func(ToolProgress))
	if fn == nil {
		return false
	}
	fn(p)
	return true
}

// FormatToolProgress renders a compact label: "42%", "3/10" or "3", plus an
// optional (bounded) message.
func FormatToolProgress(p ToolProgress) string {
	var label string
	switch {
	case p.Total > 0:
		pct := p.Progress / p.Total * 100
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		label = fmt.Sprintf("%d%%", int(pct))
	case p.Progress > 0:
		label = strconv.FormatFloat(p.Progress, 'f', -1, 64)
	}
	msg := strings.Join(strings.Fields(p.Message), " ")
	if r := []rune(msg); len(r) > 80 {
		msg = string(r[:79]) + "…"
	}
	switch {
	case label == "":
		return msg
	case msg == "":
		return label
	default:
		return label + " · " + msg
	}
}
