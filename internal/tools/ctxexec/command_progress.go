package ctxexec

import (
	"context"
	"strings"
	"time"

	"supercli/internal/tools/core"
)

// commandProgressEvery is how often a running command reports its latest
// output line as live, UI-only tool progress (a long build or test run then
// shows that it is alive instead of a silent "running" row).
var commandProgressEvery = 2 * time.Second

// reportCommandProgress forwards the newest non-empty output line whenever it
// changed, until done closes. Commands without a progress sink (no live UI)
// stop the reporter after the first attempt.
func reportCommandProgress(ctx context.Context, done <-chan struct{}, act *outputActivity) {
	t := time.NewTicker(commandProgressEvery)
	defer t.Stop()
	last := ""
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			tail, _ := act.snapshot()
			line := lastOutputLine(tail)
			if line == "" || line == last {
				continue
			}
			last = line
			if !core.ReportToolProgress(ctx, core.ToolProgress{Message: line}) {
				return
			}
		}
	}
}

// lastOutputLine is the final visible line of the output tail (ANSI codes and
// carriage-return redraws removed), or "".
func lastOutputLine(tail string) string {
	s := strings.ToValidUTF8(ansiEscape.ReplaceAllString(tail, ""), "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	raw := strings.Split(s, "\n")
	for i := len(raw) - 1; i >= 0; i-- {
		l := raw[i]
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}
