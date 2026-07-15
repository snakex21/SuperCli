package agenteval

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const ReportSchemaVersion = 1

type BenchOptions struct {
	SuitePath      string
	WorkRoot       string
	Models         []string
	TaskID         string
	AgentCommand   []string
	Timeout        time.Duration
	KeepWorkspaces bool
	Runner         AgentRunner
}

type AgentInput struct {
	Workspace string
	Prompt    string
	TaskID    string
	Model     string
}

type AgentOutput struct {
	Stdout string
	Stderr string
}

type AgentRunner func(context.Context, AgentInput) (AgentOutput, error)

type Report struct {
	SchemaVersion int         `json:"schemaVersion"`
	SuiteID       string      `json:"suiteId"`
	StartedAt     string      `json:"startedAt"`
	DurationMs    int64       `json:"durationMs"`
	Models        []string    `json:"models"`
	Passed        int         `json:"passed"`
	Failed        int         `json:"failed"`
	Runs          []RunReport `json:"runs"`
}

type RunReport struct {
	TaskID           string        `json:"taskId"`
	Model            string        `json:"model,omitempty"`
	Passed           bool          `json:"passed"`
	DurationMs       int64         `json:"durationMs"`
	ChangedFiles     []string      `json:"changedFiles,omitempty"`
	MissingChanges   []string      `json:"missingChanges,omitempty"`
	ForbiddenChanges []string      `json:"forbiddenChanges,omitempty"`
	MissingTrace     []string      `json:"missingTrace,omitempty"`
	Checks           []CheckReport `json:"checks"`
	AgentError       string        `json:"agentError,omitempty"`
	StdoutTail       string        `json:"stdoutTail,omitempty"`
	StderrTail       string        `json:"stderrTail,omitempty"`
}

type CheckReport struct {
	ID         string `json:"id"`
	Passed     bool   `json:"passed"`
	DurationMs int64  `json:"durationMs"`
	ExitCode   int    `json:"exitCode"`
	OutputTail string `json:"outputTail,omitempty"`
}

// Bench runs each selected task/model pair in an isolated fixture copy.
func Bench(ctx context.Context, options BenchOptions) (Report, error) {
	if options.Timeout <= 0 {
		options.Timeout = 10 * time.Minute
	}
	if strings.TrimSpace(options.WorkRoot) == "" {
		return Report{}, fmt.Errorf("eval work root is required")
	}
	suite, err := LoadSuite(options.SuitePath)
	if err != nil {
		return Report{}, err
	}
	models := compactStrings(options.Models)
	if len(models) == 0 {
		models = []string{""}
	}
	runner := options.Runner
	if runner == nil {
		if len(options.AgentCommand) == 0 {
			return Report{}, fmt.Errorf("agent command is required")
		}
		runner = commandRunner(options.AgentCommand)
	}
	started := time.Now()
	report := Report{SchemaVersion: ReportSchemaVersion, SuiteID: suite.ID, StartedAt: started.UTC().Format(time.RFC3339Nano), Models: compactStrings(options.Models)}
	suiteDir := filepath.Dir(options.SuitePath)
	for _, task := range suite.Tasks {
		if options.TaskID != "" && task.ID != options.TaskID {
			continue
		}
		for _, model := range models {
			run := runOne(ctx, suiteDir, options, task, model, runner)
			report.Runs = append(report.Runs, run)
			if run.Passed {
				report.Passed++
			} else {
				report.Failed++
			}
		}
	}
	if len(report.Runs) == 0 {
		return Report{}, fmt.Errorf("no eval runs selected")
	}
	report.DurationMs = time.Since(started).Milliseconds()
	return report, nil
}

func runOne(parent context.Context, suiteDir string, options BenchOptions, task Task, model string, runner AgentRunner) RunReport {
	started := time.Now()
	run := RunReport{TaskID: task.ID, Model: model}
	name := sanitizeName(task.ID + "-" + model + "-" + fmt.Sprint(time.Now().UnixNano()))
	workspace := filepath.Join(options.WorkRoot, name)
	if !options.KeepWorkspaces {
		defer os.RemoveAll(workspace)
	}
	fixture := filepath.Join(suiteDir, filepath.FromSlash(task.WorkspaceFixture))
	if err := copyTree(fixture, workspace); err != nil {
		run.AgentError = err.Error()
		run.DurationMs = time.Since(started).Milliseconds()
		return run
	}
	before, err := snapshotTree(workspace)
	if err != nil {
		run.AgentError = err.Error()
		run.DurationMs = time.Since(started).Milliseconds()
		return run
	}
	ctx, cancel := context.WithTimeout(parent, options.Timeout)
	defer cancel()
	out, agentErr := runner(ctx, AgentInput{Workspace: workspace, Prompt: task.Prompt, TaskID: task.ID, Model: model})
	if agentErr != nil {
		run.AgentError = agentErr.Error()
	}
	run.StdoutTail = tail(out.Stdout, 4096)
	run.StderrTail = tail(out.Stderr, 4096)
	after, snapErr := snapshotTree(workspace)
	if snapErr != nil && run.AgentError == "" {
		run.AgentError = snapErr.Error()
	}
	run.ChangedFiles = changedFiles(before, after)
	run.MissingChanges = missingExpected(task.ExpectedChangedFiles, run.ChangedFiles)
	run.ForbiddenChanges = intersect(task.ForbiddenChangedFiles, run.ChangedFiles)
	run.MissingTrace = missingTrace(task.RequiredTraceEvents, out.Stdout)
	for _, check := range task.VerificationCommands {
		run.Checks = append(run.Checks, runCheck(ctx, workspace, check))
	}
	run.Passed = run.AgentError == "" && len(run.MissingChanges) == 0 && len(run.ForbiddenChanges) == 0 && len(run.MissingTrace) == 0
	for _, check := range run.Checks {
		run.Passed = run.Passed && check.Passed
	}
	run.DurationMs = time.Since(started).Milliseconds()
	return run
}

func commandRunner(argv []string) AgentRunner {
	base := append([]string(nil), argv...)
	return func(ctx context.Context, input AgentInput) (AgentOutput, error) {
		expanded := make([]string, len(base))
		for i, arg := range base {
			r := strings.NewReplacer("{workspace}", input.Workspace, "{prompt}", input.Prompt, "{task_id}", input.TaskID, "{model}", input.Model)
			expanded[i] = r.Replace(arg)
		}
		cmd := exec.CommandContext(ctx, expanded[0], expanded[1:]...)
		cmd.Dir = input.Workspace
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("agent timeout after deadline: %w", ctx.Err())
		}
		return AgentOutput{Stdout: stdout.String(), Stderr: stderr.String()}, err
	}
}

func runCheck(ctx context.Context, workspace string, check VerificationCommand) CheckReport {
	started := time.Now()
	cmd := exec.CommandContext(ctx, check.Args[0], check.Args[1:]...)
	cmd.Dir = workspace
	out, err := cmd.CombinedOutput()
	report := CheckReport{ID: check.ID, Passed: err == nil, DurationMs: time.Since(started).Milliseconds(), OutputTail: tail(string(out), 4096)}
	if err != nil {
		report.ExitCode = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			report.ExitCode = ee.ExitCode()
		}
	}
	return report
}

func snapshotTree(root string) (map[string][sha256.Size]byte, error) {
	out := map[string][sha256.Size]byte{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = sha256.Sum256(data)
		return nil
	})
	return out, err
}

func changedFiles(before, after map[string][sha256.Size]byte) []string {
	set := map[string]bool{}
	for path, hash := range before {
		if got, ok := after[path]; !ok || got != hash {
			set[path] = true
		}
	}
	for path, hash := range after {
		if got, ok := before[path]; !ok || got != hash {
			set[path] = true
		}
	}
	out := make([]string, 0, len(set))
	for path := range set {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = in.Close()
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		readCloseErr := in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if readCloseErr != nil {
			return readCloseErr
		}
		return closeErr
	})
}

func missingTrace(required []string, stdout string) []string {
	found := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if typ, _ := event["type"].(string); typ != "" {
			if name, _ := event["name"].(string); name != "" {
				found[typ+":"+name] = true
			}
		}
		if typ, _ := event["event"].(string); typ != "" {
			if name, _ := event["name"].(string); name != "" {
				found[typ+":"+name] = true
			}
		}
	}
	var missing []string
	for _, want := range required {
		if !found[want] {
			missing = append(missing, want)
		}
	}
	return missing
}

func missingExpected(expected, actual []string) []string { return difference(expected, actual) }
func intersect(a, b []string) []string {
	set := stringSet(b)
	var out []string
	for _, s := range a {
		if set[filepath.ToSlash(s)] {
			out = append(out, filepath.ToSlash(s))
		}
	}
	return out
}
func difference(a, b []string) []string {
	set := stringSet(b)
	var out []string
	for _, s := range a {
		s = filepath.ToSlash(s)
		if !set[s] {
			out = append(out, s)
		}
	}
	return out
}
func stringSet(in []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range in {
		out[filepath.ToSlash(s)] = true
	}
	return out
}
func compactStrings(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if b.Len() > 100 {
		return b.String()[:100]
	}
	return b.String()
}
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "[... omitted ...]\n" + s[len(s)-n:]
}

func WriteJSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }
