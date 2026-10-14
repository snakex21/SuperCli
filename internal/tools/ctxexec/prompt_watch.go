package ctxexec

import (
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultPromptIdle is how long a command may stay silent after printing a
// prompt-like last line before the runner stops it. ctx_execute has no stdin,
// so a program waiting for an answer (menu, [y/N], password) never finishes.
// Silence alone never stops a command: long builds/tests stay untouched.
const DefaultPromptIdle = 30 * time.Second

// PromptStallPrefix starts Result.Error when the prompt watchdog stopped a run.
const PromptStallPrefix = "waiting_for_input"

const promptTailBytes = 512

// outputActivity records the last write time and a short tail of the
// combined decoded output. Both streams write concurrently.
type outputActivity struct {
	mu   sync.Mutex
	last time.Time
	tail []byte
}

func (a *outputActivity) note(p []byte) {
	if len(p) == 0 {
		return
	}
	a.mu.Lock()
	a.last = time.Now()
	a.tail = append(a.tail, p...)
	if len(a.tail) > promptTailBytes {
		a.tail = append(a.tail[:0], a.tail[len(a.tail)-promptTailBytes:]...)
	}
	a.mu.Unlock()
}

func (a *outputActivity) snapshot() (string, time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return string(a.tail), time.Since(a.last)
}

type activityWriter struct {
	dst io.Writer
	act *outputActivity
}

func (w activityWriter) Write(p []byte) (int, error) {
	w.act.note(p)
	return w.dst.Write(p)
}

// watchPrompt calls onStall once when the output has been silent for idle and
// its last line looks like an interactive prompt. It returns when done closes.
func watchPrompt(done <-chan struct{}, act *outputActivity, idle time.Duration, onStall func(line string)) {
	tick := idle / 4
	if tick > time.Second {
		tick = time.Second
	}
	if tick < 10*time.Millisecond {
		tick = 10 * time.Millisecond
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			tail, since := act.snapshot()
			if since < idle {
				continue
			}
			if line := promptLine(tail); line != "" {
				onStall(line)
				return
			}
		}
	}
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]`)

var promptMarkers = []string{
	"[y/n]", "(y/n)", "[y/n/", "(yes/no)", "[yes/no]", "(y/n/", "y/n?",
	"press any key", "press enter", "press return", "use arrow keys", "hit enter",
}

var promptWords = []string{
	"password", "passphrase", "enter", "select", "choose", "choice", "confirm",
	"continue", "username", "name", "type", "input", "value", "option",
	"overwrite", "proceed", "answer", "token", "key",
}

// promptLine returns the last non-empty output line when it looks like a
// question waiting for an answer, otherwise "".
func promptLine(tail string) string {
	s := strings.ToValidUTF8(ansiEscape.ReplaceAllString(tail, ""), "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// Collect the last few non-empty lines (a carriage return redraws a line,
	// so only its final segment counts). Interactive menus print the question
	// above the options, so strong markers are checked on all of them.
	var recent []string
	raw := strings.Split(s, "\n")
	for i := len(raw) - 1; i >= 0 && len(recent) < promptScanLines; i-- {
		l := strings.TrimRight(raw[i], "\r \t")
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		if l = strings.TrimSpace(l); l != "" {
			recent = append(recent, l)
		}
	}
	if len(recent) == 0 {
		return ""
	}
	for _, l := range recent {
		if strongPrompt(l) {
			return clipPromptLine(l)
		}
	}
	line := recent[0]
	lower := strings.ToLower(line)
	switch {
	case strings.HasSuffix(line, "?"):
		return clipPromptLine(line)
	case strings.HasSuffix(line, ":"), strings.HasSuffix(line, ">"):
		for _, w := range promptWords {
			if strings.Contains(lower, w) {
				return clipPromptLine(line)
			}
		}
	}
	return ""
}

const promptScanLines = 8

func strongPrompt(line string) bool {
	lower := strings.ToLower(line)
	for _, m := range promptMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return questionPrefix(line) || strings.HasPrefix(line, "❯") || strings.HasPrefix(line, "›")
}

// questionPrefix matches inquirer-style "? Question" lines but not
// `go test` lines such as "?   \tpkg\t[no test files]".
func questionPrefix(line string) bool {
	return len(line) > 2 && strings.HasPrefix(line, "? ") && line[2] != ' ' && line[2] != '\t'
}

func clipPromptLine(line string) string {
	if len(line) > 200 {
		line = strings.ToValidUTF8(line[len(line)-200:], "")
	}
	return line
}
