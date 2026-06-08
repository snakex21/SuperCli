package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Category classifies the source of a tool failure. The
// attribution system picks one per failed tool call; the
// downstream policy decides what to do with it.
type Category int

const (
	// CategoryUnknown means the classifier could not
	// determine the source. The default action is to
	// retry once, then surface.
	CategoryUnknown Category = iota
	// CategoryModel means the model likely produced bad
	// arguments, hallucinated a file, or chose the wrong
	// tool. Retryable, with a hint to the model.
	CategoryModel
	// CategoryProgram means the tool itself misbehaved
	// (nil pointer, type error, race). Not retryable;
	// surface to user and log to tool_errors.log.
	CategoryProgram
	// CategoryEnvironment means something outside the
	// tool is missing: binary not in PATH, network
	// unreachable, permission denied on a real path.
	// Action: ask user OR skip.
	CategoryEnvironment
)

// String returns the lowercase name of the category.
func (c Category) String() string {
	switch c {
	case CategoryModel:
		return "model"
	case CategoryProgram:
		return "program"
	case CategoryEnvironment:
		return "environment"
	default:
		return "unknown"
	}
}

// Verdict is the classifier's output: a category, a
// confidence, and a human-readable reason. The optional
// Suggestion is what to tell the user to do (e.g. "install
// ripgrep").
type Verdict struct {
	Category   Category
	Confidence float64 // 0..1
	Reason     string
	Suggestion string
}

// Action is the policy's output. See F4-design §9.4.
type Action int

const (
	// ActionRetry re-calls the tool with the same args.
	// Only used for low-confidence model errors.
	ActionRetry Action = iota
	// ActionRetryWithHint re-calls the tool but injects a
	// hint about the previous failure into the model's
	// context.
	ActionRetryWithHint
	// ActionSwitchModel asks the runtime to swap models
	// (F6 dependency; in F4 we just emit a
	// SwitchModelEvent).
	ActionSwitchModel
	// ActionLogAndSurface records the error in
	// tool_errors.log and returns it to the model as a
	// tool result with a "this is a tool bug" prefix.
	ActionLogAndSurface
	// ActionAskUser dispatches the AskUser tool with
	// prepared options.
	ActionAskUser
	// ActionSkip gives up on this step and continues.
	ActionSkip
)

// String returns the lowercase name of the action.
func (a Action) String() string {
	switch a {
	case ActionRetry:
		return "retry"
	case ActionRetryWithHint:
		return "retry_with_hint"
	case ActionSwitchModel:
		return "switch_model"
	case ActionLogAndSurface:
		return "log_and_surface"
	case ActionAskUser:
		return "ask_user"
	case ActionSkip:
		return "skip"
	default:
		return "unknown"
	}
}

// Classifier inspects a tool's input and error and produces
// a Verdict. It is a pure function over the inputs; no
// goroutines, no I/O. The heuristics are documented in
// F4-design §9.3.
type Classifier struct{}

// Classify inspects the tool name, args, and result to pick
// a category. The order of checks is documented in the
// design doc and reflected by the source order below.
func (Classifier) Classify(tool string, args json.RawMessage, result Result) Verdict {
	errStr := ""
	if result.Err != nil {
		errStr = result.Err.Error()
	}
	if errStr == "" {
		// No error -> no failure to attribute.
		return Verdict{Category: CategoryUnknown, Confidence: 0, Reason: "no error"}
	}
	// 1. Explicit environment markers.
	if v, ok := classifyEnvMarkers(errStr); ok {
		return v
	}
	// 2. JSON / schema errors → model.
	if v, ok := classifySchemaError(errStr); ok {
		return v
	}
	// 3. "not found" with a hallucinated-looking path →
	// model; otherwise environment.
	if v, ok := classifyNotFound(errStr, args); ok {
		return v
	}
	// 4. Panics → program.
	if v, ok := classifyPanic(errStr); ok {
		return v
	}
	// 5. User-tool loader errors → environment.
	if v, ok := classifyUnsafe(errStr); ok {
		return v
	}
	// 6. Default → model (low confidence).
	return Verdict{
		Category:   CategoryModel,
		Confidence: 0.5,
		Reason:     "no heuristic matched; defaulting to model",
	}
}

func classifyEnvMarkers(s string) (Verdict, bool) {
	low := strings.ToLower(s)
	// exec errors and binary not on PATH.
	if strings.Contains(low, "executable file not found") ||
		strings.Contains(low, "not found in %path%") ||
		strings.Contains(low, ": not found") {
		// Extract the binary name if possible: "exec:
		// \"rg\": executable file not found ..."
		binary := extractBinary(low)
		return Verdict{
			Category:   CategoryEnvironment,
			Confidence: 0.95,
			Reason:     "binary not in PATH",
			Suggestion: fmt.Sprintf("install %s or set SUPERCLI_USE_FALLBACK_SEARCH=1", binary),
		}, true
	}
	// Network errors.
	if strings.Contains(low, "connection refused") ||
		strings.Contains(low, "no such host") ||
		strings.Contains(low, "i/o timeout") ||
		strings.Contains(low, "tls: ") ||
		strings.Contains(low, "network is unreachable") {
		return Verdict{
			Category:   CategoryEnvironment,
			Confidence: 0.95,
			Reason:     "network error",
			Suggestion: "check connectivity or skip the task",
		}, true
	}
	// Permission denied on a directory path.
	if strings.Contains(low, "permission denied") {
		return Verdict{
			Category:   CategoryEnvironment,
			Confidence: 0.9,
			Reason:     "permission denied",
			Suggestion: "run with elevated rights or change path",
		}, true
	}
	// Context deadline (timeout from ctx.WithTimeout).
	if strings.Contains(low, "context deadline exceeded") {
		return Verdict{
			Category:   CategoryEnvironment,
			Confidence: 0.9,
			Reason:     "context deadline exceeded",
			Suggestion: "increase timeout_ms in the tool definition",
		}, true
	}
	return Verdict{}, false
}

func classifySchemaError(s string) (Verdict, bool) {
	low := strings.ToLower(s)
	if strings.Contains(low, "bad args") ||
		strings.Contains(low, "json: cannot unmarshal") ||
		strings.Contains(low, "schema") ||
		strings.Contains(low, "invalid argument") {
		return Verdict{
			Category:   CategoryModel,
			Confidence: 0.9,
			Reason:     "schema/argument violation",
		}, true
	}
	return Verdict{}, false
}

func classifyNotFound(s string, args json.RawMessage) (Verdict, bool) {
	low := strings.ToLower(s)
	if !strings.Contains(low, "not found") && !strings.Contains(low, "no such file") {
		return Verdict{}, false
	}
	// Absolute path that doesn't exist → likely model
	// hallucination. Relative / "." / "" → likely env.
	if len(args) == 0 {
		return Verdict{
			Category:   CategoryModel,
			Confidence: 0.7,
			Reason:     "not found with no args (suspicious)",
		}, true
	}
	var params map[string]any
	if err := json.Unmarshal(args, &params); err != nil {
		return Verdict{}, false
	}
	for _, v := range params {
		str, ok := v.(string)
		if !ok {
			continue
		}
		if strings.HasPrefix(str, "/") || (len(str) >= 2 && str[1] == ':') {
			return Verdict{
				Category:   CategoryModel,
				Confidence: 0.7,
				Reason:     "not found on absolute path: " + str,
			}, true
		}
	}
	return Verdict{
		Category:   CategoryEnvironment,
		Confidence: 0.6,
		Reason:     "not found on relative path",
	}, true
}

func classifyPanic(s string) (Verdict, bool) {
	if strings.Contains(s, "panic") || strings.Contains(s, "nil pointer") || strings.Contains(s, "runtime error") {
		return Verdict{
			Category:   CategoryProgram,
			Confidence: 0.99,
			Reason:     "tool panic: " + s,
		}, true
	}
	return Verdict{}, false
}

func classifyUnsafe(s string) (Verdict, bool) {
	if strings.Contains(s, "unsafe command") {
		return Verdict{
			Category:   CategoryEnvironment,
			Confidence: 0.95,
			Reason:     "user tool referenced an unsafe command",
			Suggestion: "edit the TOML to use an allow-listed command",
		}, true
	}
	return Verdict{}, false
}

// extractBinary pulls the program name from an "exec: \"X\""
// error string. Returns empty when not parseable.
func extractBinary(low string) string {
	// Common shape: exec: "rg": executable file not found
	idx := strings.Index(low, "exec:")
	if idx < 0 {
		// Unix: "rg: not found"
		idx = strings.Index(low, ": not found")
		if idx < 0 {
			return ""
		}
		end := idx
		// Walk back to whitespace or start.
		start := end - 1
		for start >= 0 && low[start] != ' ' && low[start] != '\t' {
			start--
		}
		return low[start+1 : end]
	}
	rest := low[idx+5:]
	// Skip optional whitespace and quote.
	rest = strings.TrimLeft(rest, ` "`)
	end := strings.IndexAny(rest, `":`)
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// Policy turns a Verdict and the current attempt number into
// an Action. The matrix is in F4-design §9.4.
type Policy struct{}

// Decide picks an action. attempt is 1-indexed (1 = first
// try).
func (Policy) Decide(v Verdict, attempt int) Action {
	switch v.Category {
	case CategoryModel:
		if v.Confidence < 0.7 {
			return ActionRetry
		}
		if attempt == 1 {
			return ActionRetryWithHint
		}
		return ActionLogAndSurface
	case CategoryProgram:
		return ActionLogAndSurface
	case CategoryEnvironment:
		// Recoverable env errors get a user prompt; the
		// caller decides which to use.
		return ActionAskUser
	default:
		if attempt == 1 {
			return ActionRetry
		}
		return ActionLogAndSurface
	}
}

// ErrorRecord is one entry in tool_errors.log. The
// structure is stable JSON-Lines.
type ErrorRecord struct {
	Ts         string  `json:"ts"`
	Tool       string  `json:"tool"`
	Args       string  `json:"args"`
	Category   string  `json:"category"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
	Suggestion string  `json:"suggestion,omitempty"`
	SessionID  string  `json:"session_id,omitempty"`
}

// ErrorLog is a thread-safe append-only NDJSON file. The
// loop calls Append on every attributed failure. F5 will
// scan this file to learn error patterns.
type ErrorLog struct {
	mu  sync.Mutex
	enc *json.Encoder
	w   *lineWriter
}

// NewErrorLog opens (or creates) a JSONL file at path. The
// parent directory must exist. Pass path = "" to disable
// logging.
func NewErrorLog(path string) (*ErrorLog, error) {
	if path == "" {
		return &ErrorLog{}, nil
	}
	w, err := newLineWriter(path)
	if err != nil {
		return nil, err
	}
	return &ErrorLog{
		enc: json.NewEncoder(w),
		w:   w,
	}, nil
}

// Append writes one record. The Ts field is filled in if
// empty. Errors are non-fatal: a closed log should not
// break the agent loop.
func (e *ErrorLog) Append(r ErrorRecord) {
	if e == nil || e.enc == nil {
		return
	}
	if r.Ts == "" {
		r.Ts = time.Now().UTC().Format(time.RFC3339Nano)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.enc.Encode(r)
}

// Close releases the underlying file.
func (e *ErrorLog) Close() error {
	if e == nil || e.w == nil {
		return nil
	}
	return e.w.Close()
}
