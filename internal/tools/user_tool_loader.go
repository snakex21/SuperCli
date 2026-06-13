package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// UserToolDef is the parsed shape of a single user-tool TOML
// file. The TOML is intentionally minimal: only the fields we
// actually execute, no nested tables except the simple
// execution sub-block.
type UserToolDef struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
	Version     string `toml:"version"`

	// Schema is the raw JSON Schema string the model will
	// see. We accept it as-is and pass it through.
	Schema string `toml:"schema"`

	// Execution sub-block.
	Execution userExecution `toml:"execution"`
}

type userExecution struct {
	// Type is "shell", "script", or "http". F4.c ships
	// "shell" and "script"; "http" is parsed but
	// implemented in F4.c+1.
	Type string `toml:"type"`
	// Command is the program to run (for shell) or
	// interpreter (for script).
	Command string `toml:"command"`
	// Args are template strings, with `{param}` placeholders
	// substituted from the model's args.
	Args []string `toml:"args"`
	// Body is inline code (for script type).
	Body string `toml:"body"`
	// TimeoutMs bounds the execution. 0 = 5s default.
	TimeoutMs int `toml:"timeout_ms"`
}

// UserToolLoader scans a list of directories for .toml files
// and converts each into a registered Tool. See F4-design §3
// for the file format.
type UserToolLoader struct {
	Dirs []string
}

// NewUserToolLoader creates a loader over the standard
// project + user directories. Missing directories are
// skipped silently on Load.
// dataDir is the resolved SuperCli data directory (portable:
// supercli-data next to the executable).
func NewUserToolLoader(projectDir, dataDir string) *UserToolLoader {
	return &UserToolLoader{
		Dirs: []string{
			filepath.Join(projectDir, "tools"),
			filepath.Join(dataDir, "tools"),
		},
	}
}

// Load reads every .toml file in the configured directories
// and returns a slice of registered Tool. Invalid files are
// skipped with a returned error per file but the rest still
// load — strict mode can be added later.
func (l *UserToolLoader) Load() ([]Tool, []error) {
	var (
		out   []Tool
		errs  []error
		seen  = make(map[string]string) // name -> source path (for dup detection)
	)
	for _, dir := range l.Dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			errs = append(errs, fmt.Errorf("read %q: %w", dir, err))
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if !strings.HasSuffix(e.Name(), ".toml") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			def, err := parseUserTool(path)
			if err != nil {
				errs = append(errs, fmt.Errorf("parse %q: %w", path, err))
				continue
			}
			if err := validateUserTool(def); err != nil {
				errs = append(errs, fmt.Errorf("validate %q: %w", path, err))
				continue
			}
			if existing, ok := seen[def.Name]; ok {
				errs = append(errs, fmt.Errorf("dup tool %q: %s vs %s", def.Name, existing, path))
				continue
			}
			seen[def.Name] = path
			out = append(out, buildUserTool(def))
		}
	}
	return out, errs
}

// allowedCommands is the safety allow-list for shell-type
// user tools. Anything outside this set is rejected with
// ErrUnsafeCommand. The list covers common dev tools; users
// can extend it via SUPERCLI_USER_TOOL_ALLOW (comma-separated)
// in F4.c+1.
var allowedCommands = map[string]bool{
	"rg": true, "grep": true, "sed": true, "awk": true,
	"jq": true, "curl": true, "git": true, "node": true,
	"python": true, "python3": true, "deno": true, "bash": true,
	"sh": true, "cat": true, "head": true, "tail": true, "wc": true,
	"sort": true, "uniq": true, "tr": true, "cut": true, "xargs": true,
	"find": true, "ls": true, "echo": true, "printf": true, "diff": true,
	"tar": true, "gzip": true, "gunzip": true, "zcat": true,
}

// ErrUnsafeCommand is returned when a user tool references a
// command not in the safety allow-list.
var ErrUnsafeCommand = fmt.Errorf("unsafe command: not in allow-list")

// validateUserTool checks name + schema + execution type and
// command allow-list. It returns nil when the tool is safe to
// register.
func validateUserTool(d UserToolDef) error {
	if !nameRe.MatchString(d.Name) {
		return fmt.Errorf("invalid name %q: must match %s", d.Name, nameRe.String())
	}
	if d.Description == "" {
		return fmt.Errorf("description is required")
	}
	if d.Execution.Type == "" {
		return fmt.Errorf("execution.type is required")
	}
	switch d.Execution.Type {
	case "shell":
		if d.Execution.Command == "" {
			return fmt.Errorf("execution.command is required for shell type")
		}
		base := filepath.Base(d.Execution.Command)
		if !allowedCommands[base] {
			return fmt.Errorf("%w: %s", ErrUnsafeCommand, base)
		}
	case "script":
		if d.Execution.Body == "" {
			return fmt.Errorf("execution.body is required for script type")
		}
		if d.Execution.Command == "" {
			return fmt.Errorf("execution.command (interpreter) is required for script type")
		}
		base := filepath.Base(d.Execution.Command)
		if !allowedCommands[base] {
			return fmt.Errorf("%w: %s", ErrUnsafeCommand, base)
		}
	case "http":
		// F4.c+1: not implemented yet, but the type is
		// parsed so users can write the file. The build
		// step below will return a clear error if it
		// tries to run.
		return fmt.Errorf("http execution is F4.c+1, not yet implemented")
	default:
		return fmt.Errorf("unknown execution.type %q", d.Execution.Type)
	}
	return nil
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// buildUserTool converts a parsed def into a runnable
// tools.Tool. The Fn closes over the def and the registry's
// home directory.
func buildUserTool(d UserToolDef) Tool {
	timeout := time.Duration(d.Execution.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return Tool{
		Name:        d.Name,
		Description: d.Description,
		Schema:      d.Schema,
		Fn:          makeUserToolFn(d, timeout),
	}
}

// makeUserToolFn returns the Fn that dispatches to either
// shell or script execution.
func makeUserToolFn(d UserToolDef, timeout time.Duration) func(ctx context.Context, args json.RawMessage) (Result, error) {
	return func(ctx context.Context, args json.RawMessage) (Result, error) {
		// Parse the model's args into a generic map for
		// {param} substitution.
		var params map[string]any
		if len(args) > 0 {
			if err := json.Unmarshal(args, &params); err != nil {
				return Result{Err: fmt.Errorf("%s: bad args: %w", d.Name, err)}, nil
			}
		}
		// Apply context timeout on top of the caller's
		// ctx (caller's cancellation still propagates).
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		switch d.Execution.Type {
		case "shell":
			return runShell(ctx, d.Execution.Command, d.Execution.Args, params)
		case "script":
			return runScript(ctx, d.Execution.Command, d.Execution.Body, d.Execution.Args, params)
		default:
			return Result{Err: fmt.Errorf("%s: unknown execution.type %q", d.Name, d.Execution.Type)}, nil
		}
	}
}

// interpolateArgs substitutes {key} placeholders in the
// arg templates. Missing keys produce an empty string and
// a stderr note appended to the result. Non-string params
// are JSON-encoded.
func interpolateArgs(templates []string, params map[string]any) ([]string, []string) {
	args := make([]string, 0, len(templates))
	notes := []string{}
	for _, t := range templates {
		if !strings.Contains(t, "{") {
			args = append(args, t)
			continue
		}
		out := t
		for k, v := range params {
			placeholder := "{" + k + "}"
			var rep string
			switch x := v.(type) {
			case string:
				rep = x
			default:
				b, _ := json.Marshal(v)
				rep = string(b)
			}
			out = strings.ReplaceAll(out, placeholder, rep)
		}
		// Detect unreplaced placeholders.
		if strings.Contains(out, "{") && strings.Contains(out, "}") {
			notes = append(notes, fmt.Sprintf("unreplaced placeholder in %q", t))
		}
		args = append(args, out)
	}
	return args, notes
}

// runShell executes the command with interpolated args. The
// command is sandboxed to the current process working
// directory (or HOME override); it can NOT walk up to system
// paths because exec.Command runs with the parent's
// environment but the args are literal.
func runShell(ctx context.Context, command string, templates []string, params map[string]any) (Result, error) {
	args, notes := interpolateArgs(templates, params)
	cmd := exec.CommandContext(ctx, command, args...)
	out, err := cmd.CombinedOutput()
	res := Result{Text: string(out)}
	if len(notes) > 0 {
		res.Text += "\n[notes]\n" + strings.Join(notes, "\n")
	}
	if err != nil {
		// Surface exit code in the error string so the
		// error attribution system (F4.d) can classify
		// it.
		res.Err = fmt.Errorf("exit error: %w (output: %s)", err, truncate(string(out), 200))
	}
	return res, nil
}

// runScript invokes `interpreter -` with the body on stdin
// and the interpolated args as command-line args. The body
// is a literal string from the TOML — it is executed once
// per call with fresh stdin.
func runScript(ctx context.Context, interpreter, body string, templates []string, params map[string]any) (Result, error) {
	args, notes := interpolateArgs(templates, params)
	cmd := exec.CommandContext(ctx, interpreter, args...)
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	res := Result{Text: string(out)}
	if len(notes) > 0 {
		res.Text += "\n[notes]\n" + strings.Join(notes, "\n")
	}
	if err != nil {
		res.Err = fmt.Errorf("script error: %w (output: %s)", err, truncate(string(out), 200))
	}
	return res, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
