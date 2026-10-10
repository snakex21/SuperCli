package agent

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

// Only hashes are retained, never file bodies. An observation is taken AFTER
// executing and verifying a tool: this is not a cache and cannot serve stale data.
type toolObservation struct {
	valid       bool
	comparable  bool // textual evidence sufficient for the last-resort guard only
	key, result [sha256.Size]byte
}

func observeToolResult(call llm.ToolCall, result tools.Result) toolObservation {
	if result.Err != nil || result.Image != nil || len(result.Images) > 0 {
		return toolObservation{}
	}
	text := result.Text
	if !observationReadCall(call) {
		if call.Name != "ctx_execute" {
			switch call.Name {
			case "code_intel", "recall", "search_history":
				// These textual discovery results are useful for the budget
				// guard, but do not establish a whole unchanged read round.
				return toolObservation{comparable: true, key: observationCallFingerprint(call), result: sha256.Sum256([]byte(text))}
			}
			return toolObservation{}
		}
		var args struct {
			Command []string `json:"command"`
		}
		if json.Unmarshal([]byte(call.Arguments), &args) != nil || !repeatableCheckCommand(args.Command) {
			return toolObservation{}
		}
		var output map[string]json.RawMessage
		if json.Unmarshal([]byte(text), &output) != nil {
			return toolObservation{}
		}
		var exit int
		if json.Unmarshal(output["exit_code"], &exit) != nil || exit != 0 {
			return toolObservation{}
		}
		// Runtime timing is not new evidence. Keep every other metadata field
		// (including truncation) and all substantive stdout/stderr.
		delete(output, "duration_ms")
		for _, key := range []string{"stdout", "stderr"} {
			var body string
			if json.Unmarshal(output[key], &body) == nil {
				body = strings.ReplaceAll(body, "\r\n", "\n")
				body = pythonTestDuration.ReplaceAllString(body, "${1}<elapsed>s")
				body = goTestDuration.ReplaceAllString(body, "${1}<elapsed>")
				output[key], _ = json.Marshal(body)
			}
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			return toolObservation{}
		}
		text = string(encoded)
	}
	return toolObservation{
		valid: true, comparable: true, key: observationCallFingerprint(call),
		result: sha256.Sum256([]byte(text)),
	}
}

// Refresh changes where web evidence is obtained, not the requested evidence.
// Its output hash still records any change found by a fresh read. Malformed
// values retain their identity so this never repairs or aliases invalid calls.
func observationCallFingerprint(call llm.ToolCall) [sha256.Size]byte {
	switch call.Name {
	case "web_lookup", "web_search", "web_fetch":
		var args map[string]json.RawMessage
		if json.Unmarshal([]byte(call.Arguments), &args) == nil {
			value := strings.TrimSpace(string(args["refresh"]))
			if value == "true" || value == "false" {
				delete(args, "refresh")
				if encoded, err := json.Marshal(args); err == nil {
					return toolCallFingerprint(call.Name, string(encoded))
				}
			}
		}
	}
	return toolCallFingerprint(call.Name, call.Arguments)
}

// These calls inspect evidence without changing it. A failed read can
// invalidate its own previous result without invalidating unrelated reads.
func observationReadCall(call llm.ToolCall) bool {
	switch call.Name {
	case "read_lines", "read_many", "read_context", "list_dir", "search_code",
		"read_docx", "read_pdf", "read_xlsx", "read_output", "tool_search",
		"web_lookup", "web_search", "web_fetch":
		return true
	case "read_zip":
		return toolCallKind(call.Name, call.Arguments) == "discovery"
	}
	return false
}

var pythonTestDuration = regexp.MustCompile(`(?m)^(Ran [0-9]+ tests? in )[0-9.]+s$`)
var goTestDuration = regexp.MustCompile(`(?m)^(ok[ \t]+\S+[ \t]+)(?:[0-9.]+s|\(cached\))$`)

// Shell scripts, process polling, network calls and arbitrary commands are
// deliberately excluded: their repeated output says nothing about progress.
func repeatableCheckCommand(args []string) bool {
	if len(args) < 2 {
		return false
	}
	exe := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(args[0], "\\", "/"))), ".exe")
	switch exe {
	case "go":
		return args[1] == "test" || args[1] == "vet"
	case "cargo":
		return args[1] == "test" || args[1] == "check" || args[1] == "clippy"
	case "pytest", "pytest3":
		return true
	case "python", "python3", "python3.12", "python3.13":
		if len(args) > 2 && args[1] == "-m" && (args[2] == "pytest" || args[2] == "unittest") {
			return true
		}
		path := strings.ReplaceAll(args[1], "\\", "/")
		return strings.HasSuffix(path, ".py") && strings.HasPrefix(filepath.Base(path), "test_")
	case "npm", "npm.cmd", "pnpm", "pnpm.cmd":
		return args[1] == "test"
	case "git":
		// These forms inspect state. Unknown flags/subcommands stay barriers.
		return args[1] == "status" || args[1] == "rev-parse"
	}
	return false
}

const observationHistoryLimit = 128

type seenObservation struct {
	result [sha256.Size]byte
	count  int
}
type unchangedProgress struct {
	seen    map[[sha256.Size]byte]seenObservation
	order   [][sha256.Size]byte
	rounds  int
	repeats int
	tool    string
}

// A changed result or a new observation breaks the round-wide streak while
// each read also retains its own count. Mutations and unknown side effects
// clear prior evidence; a failed known read invalidates only its own key.
func (p *unchangedProgress) observe(calls []llm.ToolCall, outcomes []callOutcome) {
	p.repeats, p.tool = 0, ""
	allRepeated := len(calls) > 0
	var repeatedKey [sha256.Size]byte
	for i, call := range calls {
		o := outcomeAt(outcomes, i)
		if o.failed && observationReadCall(call) {
			key := observationCallFingerprint(call)
			p.forget(key)
			if key == repeatedKey {
				p.repeats, p.tool = 0, ""
			}
			allRepeated = false
			continue
		}
		if o.failed || !o.observation.valid {
			*p = unchangedProgress{}
			allRepeated = false
			continue
		}
		if p.seen == nil {
			p.seen = make(map[[sha256.Size]byte]seenObservation)
		}
		ob := o.observation
		prev, found := p.seen[ob.key]
		if found && prev.result == ob.result {
			prev.count++
			if prev.count > p.repeats {
				p.repeats, p.tool = prev.count, call.Name
				repeatedKey = ob.key
			}
		} else {
			allRepeated = false
			prev = seenObservation{result: ob.result, count: 1}
			if ob.key == repeatedKey {
				p.repeats, p.tool = 0, ""
			}
		}
		if !found {
			if len(p.order) >= observationHistoryLimit {
				delete(p.seen, p.order[0])
				p.order = p.order[1:]
			}
			p.order = append(p.order, ob.key)
		}
		p.seen[ob.key] = prev
	}
	if allRepeated {
		p.rounds++
	} else {
		p.rounds = 0
	}
}

func (p *unchangedProgress) forget(key [sha256.Size]byte) {
	if _, found := p.seen[key]; !found {
		return
	}
	delete(p.seen, key)
	for i, old := range p.order {
		if old == key {
			p.order = append(p.order[:i], p.order[i+1:]...)
			break
		}
	}
}
