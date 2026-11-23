package agent

import (
	"regexp"
	"strings"
	"sync"
)

// deadEndLimit is how many failures of file-changing tools with the same
// kind of error, in one worker run, mean the environment refuses the work:
// retrying with other arguments only burns steps (Worker 312 spent 300 steps
// on 214 refused writes). It is a backstop that ends a hopeless run with one
// clear report, not a gate on ordinary failures.
const deadEndLimit = 5

// deadEndStopText is the worker's stop reason (the GUI translates it).
const deadEndStopText = "SuperCli stopped this worker: %d file changes in a row failed with the same error, so retrying would not help."

// deadEndTracker counts failed file changes by error class. Reads and other
// non-changing tools in between do not reset it; a successful file change or
// a different error does.
type deadEndTracker struct {
	mu     sync.Mutex
	class  string
	sample string
	tool   string
	count  int
}

var (
	deadEndQuoted = regexp.MustCompile(`"[^"]*"|'[^']*'|` + "`[^`]*`")
	deadEndPath   = regexp.MustCompile(`\S*[/\\]\S*`)
	deadEndNumber = regexp.MustCompile(`\d+`)
	deadEndSpace  = regexp.MustCompile(`\s+`)
)

// deadEndClass reduces an error to its kind: names, paths and numbers that
// differ between attempts are dropped.
func deadEndClass(text string) string {
	s := strings.ToLower(strings.TrimSpace(text))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = deadEndQuoted.ReplaceAllString(s, "_")
	s = deadEndPath.ReplaceAllString(s, "_")
	s = deadEndNumber.ReplaceAllString(s, "#")
	s = deadEndSpace.ReplaceAllString(s, " ")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

func (d *deadEndTracker) failure(tool, errText string) {
	class := deadEndClass(errText)
	if class == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if class != d.class {
		d.class, d.count = class, 0
	}
	d.count++
	d.sample, d.tool = strings.TrimSpace(errText), tool
}

func (d *deadEndTracker) success() {
	d.mu.Lock()
	d.class, d.sample, d.tool, d.count = "", "", "", 0
	d.mu.Unlock()
}

// due reports a dead end once: the count, the last tool and its error.
func (d *deadEndTracker) due() (count int, tool, sample string, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.count < deadEndLimit {
		return 0, "", "", false
	}
	count, tool, sample = d.count, d.tool, d.sample
	d.class, d.sample, d.tool, d.count = "", "", "", 0
	return count, tool, sample, true
}
