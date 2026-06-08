package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestClassify_EnvMarker_RgNotFound(t *testing.T) {
	v := Classifier{}.Classify("search_code", nil, Result{Err: errStr(`exec: "rg": executable file not found in $PATH`)})
	if v.Category != CategoryEnvironment {
		t.Errorf("Category = %s, want environment", v.Category)
	}
	if v.Confidence < 0.9 {
		t.Errorf("Confidence = %f, want >= 0.9", v.Confidence)
	}
}

func TestClassify_EnvMarker_PermissionDenied(t *testing.T) {
	v := Classifier{}.Classify("read_image", nil, Result{Err: errStr("open /root/secret: permission denied")})
	if v.Category != CategoryEnvironment {
		t.Errorf("Category = %s, want environment", v.Category)
	}
}

func TestClassify_EnvMarker_NetworkTimeout(t *testing.T) {
	v := Classifier{}.Classify("mcp_http", nil, Result{Err: errStr("dial tcp: i/o timeout")})
	if v.Category != CategoryEnvironment {
		t.Errorf("Category = %s, want environment", v.Category)
	}
}

func TestClassify_EnvMarker_ContextDeadline(t *testing.T) {
	v := Classifier{}.Classify("user_tool", nil, Result{Err: errStr("context deadline exceeded")})
	if v.Category != CategoryEnvironment {
		t.Errorf("Category = %s, want environment", v.Category)
	}
}

func TestClassify_ModelError_BadJSON(t *testing.T) {
	v := Classifier{}.Classify("any", json.RawMessage(`{"x":1}`), Result{Err: errStr("bad args: unexpected end of JSON")})
	if v.Category != CategoryModel {
		t.Errorf("Category = %s, want model", v.Category)
	}
}

func TestClassify_ModelError_InvalidSchema(t *testing.T) {
	v := Classifier{}.Classify("any", nil, Result{Err: errStr("schema validation: missing field 'query'")})
	if v.Category != CategoryModel {
		t.Errorf("Category = %s, want model", v.Category)
	}
}

func TestClassify_ProgramError_Panic(t *testing.T) {
	v := Classifier{}.Classify("read_image", nil, Result{Err: errStr("panic: nil pointer dereference")})
	if v.Category != CategoryProgram {
		t.Errorf("Category = %s, want program", v.Category)
	}
	if v.Confidence < 0.9 {
		t.Errorf("Confidence = %f, want >= 0.9", v.Confidence)
	}
}

func TestClassify_Ambiguous_DefaultToModel(t *testing.T) {
	v := Classifier{}.Classify("any", nil, Result{Err: errStr("some weird thing happened")})
	if v.Category != CategoryModel {
		t.Errorf("Category = %s, want model (default)", v.Category)
	}
	if v.Confidence != 0.5 {
		t.Errorf("Confidence = %f, want 0.5 (default)", v.Confidence)
	}
}

func TestClassify_NotFound_AbsolutePath_IsModel(t *testing.T) {
	args := json.RawMessage(`{"path":"/etc/shadow"}`)
	v := Classifier{}.Classify("read_image", args, Result{Err: errStr("open /etc/shadow: no such file")})
	if v.Category != CategoryModel {
		t.Errorf("Category = %s, want model (absolute path)", v.Category)
	}
}

func TestClassify_NotFound_WindowsAbsolutePath_IsModel(t *testing.T) {
	args := json.RawMessage(`{"path":"C:\\nope.txt"}`)
	v := Classifier{}.Classify("read_image", args, Result{Err: errStr("open C:\\nope.txt: The system cannot find the file specified.")})
	if v.Category != CategoryModel {
		t.Errorf("Category = %s, want model (Windows absolute)", v.Category)
	}
}

func TestClassify_NotFound_RelativePath_IsEnvironment(t *testing.T) {
	args := json.RawMessage(`{"path":"missing.txt"}`)
	v := Classifier{}.Classify("read_image", args, Result{Err: errStr("no such file")})
	if v.Category != CategoryEnvironment {
		t.Errorf("Category = %s, want environment (relative path)", v.Category)
	}
}

func TestClassify_NoError_UnknownZeroConfidence(t *testing.T) {
	v := Classifier{}.Classify("any", nil, Result{})
	if v.Category != CategoryUnknown {
		t.Errorf("Category = %s, want unknown", v.Category)
	}
	if v.Confidence != 0 {
		t.Errorf("Confidence = %f, want 0", v.Confidence)
	}
}

func TestPolicy_ModelLowConf_Retry(t *testing.T) {
	v := Verdict{Category: CategoryModel, Confidence: 0.4}
	a := Policy{}.Decide(v, 1)
	if a != ActionRetry {
		t.Errorf("Action = %s, want retry", a)
	}
}

func TestPolicy_ModelHighConf_FirstAttempt_RetryWithHint(t *testing.T) {
	v := Verdict{Category: CategoryModel, Confidence: 0.9}
	a := Policy{}.Decide(v, 1)
	if a != ActionRetryWithHint {
		t.Errorf("Action = %s, want retry_with_hint", a)
	}
}

func TestPolicy_ModelHighConf_SecondAttempt_LogAndSurface(t *testing.T) {
	v := Verdict{Category: CategoryModel, Confidence: 0.9}
	a := Policy{}.Decide(v, 2)
	if a != ActionLogAndSurface {
		t.Errorf("Action = %s, want log_and_surface", a)
	}
}

func TestPolicy_Program_AlwaysLogAndSurface(t *testing.T) {
	v := Verdict{Category: CategoryProgram, Confidence: 0.99}
	for _, attempt := range []int{1, 2, 3} {
		a := Policy{}.Decide(v, attempt)
		if a != ActionLogAndSurface {
			t.Errorf("attempt %d: Action = %s, want log_and_surface", attempt, a)
		}
	}
}

func TestPolicy_Env_AskUser(t *testing.T) {
	v := Verdict{Category: CategoryEnvironment, Confidence: 0.9}
	a := Policy{}.Decide(v, 1)
	if a != ActionAskUser {
		t.Errorf("Action = %s, want ask_user", a)
	}
}

func TestPolicy_Unknown_FirstRetry(t *testing.T) {
	v := Verdict{Category: CategoryUnknown, Confidence: 0}
	a := Policy{}.Decide(v, 1)
	if a != ActionRetry {
		t.Errorf("Action = %s, want retry", a)
	}
}

func TestPolicy_Unknown_SecondSurface(t *testing.T) {
	v := Verdict{Category: CategoryUnknown, Confidence: 0}
	a := Policy{}.Decide(v, 2)
	if a != ActionLogAndSurface {
		t.Errorf("Action = %s, want log_and_surface", a)
	}
}

func TestExtractBinary(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`exec: "rg": executable file not found`, "rg"},
		{`exec: "python3": not found`, "python3"},
		{"some rg: not found in path", "rg"},
		{"no binary here", ""},
	}
	for _, c := range cases {
		if got := extractBinary(c.in); got != c.want {
			t.Errorf("extractBinary(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestErrorLog_AppendsNDJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool_errors.log")
	log, err := NewErrorLog(path)
	if err != nil {
		t.Fatalf("NewErrorLog: %v", err)
	}
	defer log.Close()
	log.Append(ErrorRecord{Tool: "read_image", Category: "program", Reason: "panic", Confidence: 0.99})
	log.Append(ErrorRecord{Tool: "search_code", Category: "environment", Reason: "rg not found", Confidence: 0.95})
	log.Close()
	// Read back, verify two JSONL lines.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	for _, l := range lines {
		var r ErrorRecord
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Errorf("line not valid JSON: %q (%v)", l, err)
		}
		if r.Ts == "" {
			t.Errorf("Ts empty in %q", l)
		}
	}
}

func TestErrorLog_DisabledWhenPathEmpty(t *testing.T) {
	log, err := NewErrorLog("")
	if err != nil {
		t.Fatalf("NewErrorLog(\"\"): %v", err)
	}
	// Should be a no-op, not panic.
	log.Append(ErrorRecord{Tool: "x", Category: "model", Confidence: 0.5})
	_ = log.Close()
}

func TestErrorLog_ConcurrentSafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool_errors.log")
	log, _ := NewErrorLog(path)
	defer log.Close()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			log.Append(ErrorRecord{Tool: "t", Category: "model", Reason: "x"})
		}(i)
	}
	wg.Wait()
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 50 {
		t.Errorf("lines = %d, want 50", len(lines))
	}
}

// errStr is a tiny helper to construct a non-nil error.
type strErr string

func (s strErr) Error() string { return string(s) }
func errStr(s string) error    { return strErr(s) }
