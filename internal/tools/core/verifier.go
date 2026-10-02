package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Check is the input to a Verifier. Family tells the
// DefaultVerifier which heuristic to apply; Tool is the
// name (for logging); Args is the raw model JSON; Result is
// what the tool produced.
type Check struct {
	Family string
	Tool   string
	Args   json.RawMessage
	Result Result
	// BaseDir is the directory relative paths in Args resolve
	// against (the tool's home). When set, file_write/read
	// verification stats BaseDir/path instead of CWD/path —
	// without it, a relative path like "demo/x.txt" is checked
	// against the process CWD and falsely reported missing,
	// which makes the model loop trying to "fix" a file that is
	// actually fine. Empty preserves the old CWD-relative behaviour.
	BaseDir string
}

// VerifyVerdict reports the verification outcome. When OK is
// false, Reason must be non-empty.
type VerifyVerdict struct {
	OK     bool
	Reason string
}

// Verifier is implemented by any function or struct that
// can check a tool's result. The DefaultVerifier applies
// family heuristics; custom checks come from a tool's
// optional Verify field.
type Verifier interface {
	Verify(Check) VerifyVerdict
}

// DefaultVerifier applies family-specific checks. It is
// pure: no I/O for unknown families. The file_write and
// bash families DO touch the disk / spawn a probe to verify
// shape; the F4.e doc explains why (we cannot trust the
// tool's "success" claim).
type DefaultVerifier struct{}

// Verify inspects c and returns a VerifyVerdict. The order of
// checks: explicit Family first, then tool-name inference,
// then no-op.
func (DefaultVerifier) Verify(c Check) VerifyVerdict {
	family := c.Family
	if family == "" {
		family = inferFamily(c.Tool, c.Args, c.Result)
	}
	switch family {
	case "file_write":
		return verifyFileWrite(c)
	case "bash":
		return verifyBash(c)
	case "search":
		return verifySearch(c)
	case "read":
		return verifyRead(c)
	default:
		return VerifyVerdict{OK: true}
	}
}

// inferFamily picks a family based on tool name + result
// shape. New tools can override via the Check.Family field.
func inferFamily(tool string, args json.RawMessage, r Result) string {
	n := strings.ToLower(tool)
	switch {
	case strings.HasSuffix(n, "_write"), strings.HasSuffix(n, "_edit"),
		n == "write_file", n == "patch_file", n == "create_file":
		return "file_write"
	case strings.HasSuffix(n, "_bash"), strings.HasSuffix(n, "_exec"), n == "bash":
		return "bash"
	case strings.HasPrefix(n, "search_"), n == "tool_search":
		return "search"
	case strings.HasPrefix(n, "read_"):
		return "read"
	}
	// Result-shape fallback: an empty Text + non-nil Err
	// is a "search" miss; an Image-result tool is "read".
	if r.Image != nil || len(r.Images) > 0 {
		return "read"
	}
	return ""
}

// verifyFileWrite checks the file actually exists and (when
// args specify it) contains the expected substring. The
// path is read from args; we accept "path", "file", or
// "destination" as conventional names.
func verifyFileWrite(c Check) VerifyVerdict {
	if c.Result.Err != nil {
		// Tool already failed; verification is moot.
		return VerifyVerdict{OK: true}
	}
	args := decodeVerificationArgs(c.Args)
	path, ok := extractPath(args)
	if !ok {
		// No path in args; we cannot verify, so pass.
		return VerifyVerdict{OK: true}
	}
	path = resolveForVerify(c.BaseDir, path)
	info, err := os.Stat(path)
	if err != nil {
		return VerifyVerdict{OK: false, Reason: fmt.Sprintf("verification failed: file does not exist: %s", path)}
	}
	if info.Size() == 0 {
		// File tools know the exact computed post-write content, even for a
		// batch that creates and then removes text. A requested empty result
		// is useful evidence, not a failed mutation requiring another edit.
		expectedEmpty := c.Result.EmptyFileExpected
		if strings.EqualFold(c.Tool, "write_file") {
			if content, present := extractStringArg(args, "content"); present && content == "" {
				expectedEmpty = true // compatibility for older/custom write tools
			}
		}
		if !expectedEmpty {
			return VerifyVerdict{OK: false, Reason: fmt.Sprintf("verification failed: file %s is empty", path)}
		}
	}
	if want, ok := extractExpectedContent(args); ok && want != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return VerifyVerdict{OK: false, Reason: fmt.Sprintf("verification failed: cannot read %s: %v", path, err)}
		}
		if !strings.Contains(string(data), want) {
			return VerifyVerdict{OK: false, Reason: fmt.Sprintf("verification failed: %s does not contain %q", path, want)}
		}
	}
	return VerifyVerdict{OK: true}
}

// verifyBash checks that the result's text is non-empty
// (a successful command usually has SOME output) and that
// no "exit error" is in the result. The actual exit code is
// surfaced in Result.Err when non-zero, so we trust that.
func verifyBash(c Check) VerifyVerdict {
	if c.Result.Err != nil {
		return VerifyVerdict{OK: true}
	}
	if strings.TrimSpace(c.Result.Text) == "" {
		return VerifyVerdict{OK: false, Reason: "verification failed: bash produced no output"}
	}
	return VerifyVerdict{OK: true}
}

// verifySearch requires a reported outcome. No matches is useful evidence,
// not a tool failure. The tool reports backend errors through Result.Err;
// interpreting prose prefixes also misclassifies paths such as "not found.go".
func verifySearch(c Check) VerifyVerdict {
	if c.Result.Err != nil {
		return VerifyVerdict{OK: true}
	}
	t := strings.TrimSpace(c.Result.Text)
	if t == "" {
		return VerifyVerdict{OK: false, Reason: "verification failed: search returned no results"}
	}
	return VerifyVerdict{OK: true}
}

// verifyRead checks that the tool produced a non-empty
// result. Image and text both count; size > 0 is required.
func verifyRead(c Check) VerifyVerdict {
	if c.Result.Err != nil {
		return VerifyVerdict{OK: true}
	}
	if len(c.Result.Images) > 0 {
		for _, image := range c.Result.Images {
			if image == nil || len(image.Data) == 0 {
				return VerifyVerdict{OK: false, Reason: "verification failed: image is empty"}
			}
		}
		return VerifyVerdict{OK: true}
	}
	if c.Result.Image != nil {
		if len(c.Result.Image.Data) == 0 {
			return VerifyVerdict{OK: false, Reason: "verification failed: image is empty"}
		}
		return VerifyVerdict{OK: true}
	}
	if strings.TrimSpace(c.Result.Text) == "" {
		// Reading an existing zero-byte file is a valid, useful result. The
		// empty response describes the file faithfully and must not be shown as
		// a failed tool call in the chat.
		if path, ok := extractPath(decodeVerificationArgs(c.Args)); ok {
			info, err := os.Stat(resolveForVerify(c.BaseDir, path))
			if err == nil && info.Mode().IsRegular() && info.Size() == 0 {
				return VerifyVerdict{OK: true}
			}
		}
		return VerifyVerdict{OK: false, Reason: "verification failed: read returned empty content"}
	}
	return VerifyVerdict{OK: true}
}

// resolveForVerify resolves a path from tool args against base
// (the tool's home) so verification stats the same file the tool
// actually wrote/read. Absolute paths are returned unchanged; an
// empty base preserves the legacy CWD-relative behaviour.
func resolveForVerify(base, path string) string {
	if base == "" || path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}

// decodeVerificationArgs parses once for the shared path/content/expectation
// checks. Large write or patch payloads must not be decoded again for each key.
// A map retains exact key matching and the existing duplicate-key semantics.
func decodeVerificationArgs(args json.RawMessage) map[string]any {
	if len(args) == 0 {
		return nil
	}
	var object map[string]any
	if json.Unmarshal(args, &object) != nil {
		return nil
	}
	return object
}

// extractPath pulls the path from common arg names.
func extractPath(args map[string]any) (string, bool) {
	for _, key := range []string{"path", "file", "destination", "dest", "filepath"} {
		if value, ok := args[key].(string); ok && value != "" {
			return value, true
		}
	}
	return "", false
}

func extractStringArg(args map[string]any, key string) (string, bool) {
	value, ok := args[key].(string)
	return value, ok
}

// extractExpectedContent returns the expected_content /
// must_contain / contains field from args, if any.
func extractExpectedContent(args map[string]any) (string, bool) {
	for _, key := range []string{"expected_content", "must_contain", "contains", "expected"} {
		if value, ok := args[key].(string); ok {
			return value, true
		}
	}
	return "", false
}

// VerifyFn is the optional per-tool override declared on
// Tool.Verify. The agent loop calls it after Fn when set.
type VerifyFn func(Result) VerifyVerdict

// ApplyVerification runs c.Result through the per-tool
// override (if any) and otherwise the DefaultVerifier. On
// failure, it rewrites the result to surface the reason
// instead of the (potentially lying) tool output. The
// returned Result replaces the tool's output before it
// reaches the model.
//
// Exposed (capitalized) so other packages — most notably
// the agent loop — can call it without a re-export.
func ApplyVerification(c Check, override VerifyFn) Result {
	if override != nil {
		v := override(c.Result)
		if !v.OK {
			return rewriteResultForFailure(c.Result, v.Reason)
		}
		return c.Result
	}
	v := DefaultVerifier{}.Verify(c)
	if !v.OK {
		return rewriteResultForFailure(c.Result, v.Reason)
	}
	return c.Result
}

func rewriteResultForFailure(orig Result, reason string) Result {
	body := "[verification failed] " + reason
	if orig.Text != "" {
		body += "\n--- tool returned ---\n" + orig.Text
	}
	if orig.Image != nil || len(orig.Images) > 0 {
		// Keep the image; the model may want to see what
		// was claimed. Add the reason as text.
		return Result{
			Text:   body,
			Image:  orig.Image,
			Images: orig.Images,
			Err:    fmt.Errorf("%s", reason),
		}
	}
	return Result{Text: body, Err: fmt.Errorf("%s", reason)}
}
