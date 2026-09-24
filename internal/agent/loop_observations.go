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
	key, result [sha256.Size]byte
}

func observeToolResult(call llm.ToolCall, result tools.Result) toolObservation {
	if result.Err != nil || result.Image != nil {
		return toolObservation{}
	}
	text := result.Text
	switch call.Name {
	case "read_lines", "read_many", "read_context", "list_dir", "search_code",
		"read_docx", "read_pdf", "read_xlsx", "read_zip", "read_output", "tool_search":
	case "ctx_execute":
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
	default:
		return toolObservation{}
	}
	return toolObservation{
		valid: true, key: toolCallFingerprint(call.Name, call.Arguments),
		result: sha256.Sum256([]byte(text)),
	}
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

// A changed result or a new observation breaks the no-progress streak.
// Mutations, failures and unknown side effects also clear prior evidence, so
// rechecking after an edit, worker, shell command or user question is legitimate.
func (p *unchangedProgress) observe(calls []llm.ToolCall, outcomes []callOutcome) {
	p.repeats, p.tool = 0, ""
	for i := range calls {
		o := outcomeAt(outcomes, i)
		if o.failed || !o.observation.valid {
			*p = unchangedProgress{}
			return
		}
	}
	if p.seen == nil {
		p.seen = make(map[[sha256.Size]byte]seenObservation)
	}
	allRepeated := len(calls) > 0
	for i, call := range calls {
		ob := outcomes[i].observation
		prev, found := p.seen[ob.key]
		if found && prev.result == ob.result {
			prev.count++
			if prev.count > p.repeats {
				p.repeats, p.tool = prev.count, call.Name
			}
		} else {
			allRepeated = false
			prev = seenObservation{result: ob.result, count: 1}
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
