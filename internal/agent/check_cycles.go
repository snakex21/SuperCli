package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// checkCycles detects edits that flip-flop between versions already shown to
// fail. The repeat gates key on identical calls and reset after every edit,
// so A -> test fails -> B -> test fails -> A -> same failure was invisible (eval
// impossible-spec: 7 alternations, 24 calls). Here each failed verification is
// keyed by the command, the content of the files edited in this Run, and the
// normalized failure output. Seeing a key again means the model rebuilt a
// version it already tested; the tool result says so. After
// checkCycleStopRepeats such repeats the Run is steered to a final answer.
type checkCycles struct {
	mu       sync.Mutex
	touched  map[string]struct{}
	seen     map[[sha256.Size]byte]checkCycleRecord
	repeats  int
	notes    map[string]string // tool call ID -> note for its result
	stopped  bool              // the stop was already issued
	stopNote string            // appended once to the final answer
}

type checkCycleRecord struct {
	step  int64
	count int
}

const (
	checkCycleStopRepeats = 3
	checkCycleMaxFiles    = 64
	checkCycleMaxFileSize = 4 << 20
)

const checkCycleStopText = "SuperCli stopped the edit loop: the code went back to versions that had already failed the same check with the same output %d times. The requirements or tests may contradict each other."

func (c *checkCycles) reset() {
	c.mu.Lock()
	c.touched, c.seen, c.notes = nil, nil, nil
	c.repeats, c.stopped, c.stopNote = 0, false, ""
	c.mu.Unlock()
}

// touch records the files a successful file mutation named.
func (c *checkCycles) touch(baseDir, args string) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(args), &fields) != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range []string{"path", "file", "target", "destination"} {
		var path string
		if json.Unmarshal(fields[key], &path) != nil || strings.TrimSpace(path) == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		if c.touched == nil {
			c.touched = make(map[string]struct{})
		}
		if len(c.touched) < checkCycleMaxFiles {
			c.touched[filepath.Clean(path)] = struct{}{}
		}
	}
}

var checkCycleNoise = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m)\b|\(cached\)|0x[0-9a-fA-F]+`)

// observe records one finished verification. When the same code state
// failed the same way before, it queues a note for this call's tool result.
func (c *checkCycles) observe(callID string, command []string, failed bool, output string, step int64) {
	if !failed {
		return
	}
	c.mu.Lock()
	paths := make([]string, 0, len(c.touched))
	for path := range c.touched {
		paths = append(paths, path)
	}
	c.mu.Unlock()
	if len(paths) == 0 {
		return // no edits yet: identical reruns are the repeat gate's job
	}
	sort.Strings(paths)
	h := sha256.New()
	fmt.Fprintf(h, "%q\x00", command)
	for _, path := range paths {
		fmt.Fprintf(h, "%s\x00", path)
		if info, err := os.Stat(path); err != nil || info.Size() > checkCycleMaxFileSize {
			h.Write([]byte("-\x00"))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			h.Write([]byte("-\x00"))
			continue
		}
		sum := sha256.Sum256(data)
		h.Write(sum[:])
	}
	h.Write([]byte(checkCycleNoise.ReplaceAllString(output, "#")))
	var key [sha256.Size]byte
	copy(key[:], h.Sum(nil))

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = make(map[[sha256.Size]byte]checkCycleRecord)
	}
	record, known := c.seen[key]
	if !known {
		c.seen[key] = checkCycleRecord{step: step, count: 1}
		return
	}
	record.count++
	c.seen[key] = record
	c.repeats++
	if c.notes == nil {
		c.notes = make(map[string]string)
	}
	c.notes[callID] = fmt.Sprintf("[loop] This exact version of the edited files already failed this check with the same output at step %d (now %d times). "+
		"Your edits are going back and forth. Consider that the tests or requirements may contradict each other, or rethink the approach; do not re-apply an earlier version.",
		record.step, record.count)
}

// note returns and clears the note for a tool call.
func (c *checkCycles) note(callID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	note := c.notes[callID]
	delete(c.notes, callID)
	return note
}

// stopDue reports, once, that the repeat cap was reached.
func (c *checkCycles) stopDue() (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.repeats < checkCycleStopRepeats {
		return 0, false
	}
	c.stopped = true
	c.stopNote = fmt.Sprintf(checkCycleStopText, c.repeats)
	return c.repeats, true
}

// takeStopNote returns the stop message once, for the final answer.
func (c *checkCycles) takeStopNote() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	note := c.stopNote
	c.stopNote = ""
	return note
}

// verificationOutput is the text a check result shows, for cycle keys.
func verificationOutput(stdout, stderr, text string, err error) string {
	out := stdout + "\x00" + stderr + "\x00" + text
	if err != nil {
		out += "\x00" + err.Error()
	}
	return out
}

// insertBeforeOutputEnvelope adds a line to a tool result without moving
// the stored-output reference, which parsers expect on the last line.
func insertBeforeOutputEnvelope(content, line string) string {
	i := strings.LastIndexByte(content, '\n')
	if i >= 0 && strings.HasPrefix(content[i+1:], "[stored tool output: ") {
		return content[:i+1] + line + content[i:]
	}
	return content + "\n" + line
}
