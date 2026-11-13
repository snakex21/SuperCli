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
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"supercli/internal/system/childproc"
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
	// Jobs is how many task/model pairs run side by side (default 1).
	Jobs int

	// Repeats runs every selected task/model pair N times (default 1)
	// to measure variance. Repeat r of every task finishes before
	// repeat r+1 starts, so an interrupted bench still covers the suite.
	Repeats int
	// TaskIDs and Tags narrow the selection (TaskID is kept for
	// compatibility). A task matches Tags when it carries any of them
	// or its Category equals one of them.
	TaskIDs []string
	Tags    []string
	// SkipNetwork drops tasks marked network.
	SkipNetwork bool
	// NestWorkspace lays a run out as <run>/workspace plus <run>/data
	// (passed to the runner as DataDir) and reports any file the agent
	// creates in <run> outside those two directories as an outside
	// write.
	NestWorkspace bool
	// CheckTimeout bounds each verification command (default 5m). It
	// is independent of the agent timeout, so a timed-out agent is
	// still graded.
	CheckTimeout time.Duration
	// OnRunStart / OnRunDone observe progress (called from workers).
	OnRunStart func(task Task, model string, repeat int)
	OnRunDone  func(RunReport)
	// ShouldStop is consulted before each run; once it returns true
	// the remaining runs are reported as skipped (budget guard).
	ShouldStop func() bool
}

type AgentInput struct {
	Workspace string
	Prompt    string
	TaskID    string
	Model     string
	// RunDir and DataDir are set with NestWorkspace.
	RunDir  string
	DataDir string
	Repeat  int
	Task    Task
}

type AgentOutput struct {
	Stdout string
	Stderr string
	// Answer is the final assistant text when the runner knows it
	// (e.g. from the session store); otherwise it is derived from
	// Stdout with ExtractAnswer.
	Answer string
	// Extra carries runner-specific diagnostics into the report.
	Extra map[string]any
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
	Partial       int         `json:"partial,omitempty"`
	Skipped       int         `json:"skipped,omitempty"`
	Repeats       int         `json:"repeats,omitempty"`
	Runs          []RunReport `json:"runs"`
	// Summary aggregates runs per model (see summarize).
	Summary []ModelSummary `json:"summary,omitempty"`
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
	// Metrics is the agent's last `{"type":"usage"}` stdout line
	// (supercli --batch --trace-jsonl); nil when none was emitted.
	Metrics *RunMetrics `json:"metrics,omitempty"`

	Repeat            int            `json:"repeat,omitempty"`
	Category          string         `json:"category,omitempty"`
	Status            string         `json:"status,omitempty"`
	Score             float64        `json:"score"`
	FailureCategory   string         `json:"failureCategory,omitempty"`
	DisallowedChanges []string       `json:"disallowedChanges,omitempty"`
	OutsideWrites     []string       `json:"outsideWrites,omitempty"`
	TimedOut          bool           `json:"timedOut,omitempty"`
	ClaimsSuccess     bool           `json:"claimsSuccess,omitempty"`
	Answer            string         `json:"answer,omitempty"`
	Extra             map[string]any `json:"extra,omitempty"`
}

type CheckReport struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind,omitempty"`
	Passed     bool    `json:"passed"`
	Score      float64 `json:"score"`
	Weight     float64 `json:"weight,omitempty"`
	Critical   bool    `json:"critical,omitempty"`
	DurationMs int64   `json:"durationMs"`
	ExitCode   int     `json:"exitCode"`
	OutputTail string  `json:"outputTail,omitempty"`
	Detail     string  `json:"detail,omitempty"`
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
	type pair struct {
		task   Task
		model  string
		repeat int
	}
	repeats := options.Repeats
	if repeats < 1 {
		repeats = 1
	}
	report.Repeats = repeats
	var pairs []pair
	for r := 1; r <= repeats; r++ {
		for _, task := range suite.Tasks {
			if !selectTask(task, options) {
				continue
			}
			for _, model := range models {
				pairs = append(pairs, pair{task, model, r})
			}
		}
	}
	if len(pairs) == 0 {
		return Report{}, fmt.Errorf("no eval runs selected")
	}
	jobs := options.Jobs
	if jobs < 1 {
		jobs = 1
	}
	if jobs > len(pairs) {
		jobs = len(pairs)
	}
	// Runs keep suite order regardless of which worker finishes first.
	report.Runs = make([]RunReport, len(pairs))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				p := pairs[i]
				if (options.ShouldStop != nil && options.ShouldStop()) || ctx.Err() != nil {
					report.Runs[i] = RunReport{TaskID: p.task.ID, Model: p.model, Repeat: p.repeat, Category: p.task.Category, Status: StatusSkipped}
					continue
				}
				if options.OnRunStart != nil {
					options.OnRunStart(p.task, p.model, p.repeat)
				}
				report.Runs[i] = runOne(ctx, suiteDir, options, p.task, p.model, p.repeat, suite.IgnoreChangedFiles, runner)
				if options.OnRunDone != nil {
					options.OnRunDone(report.Runs[i])
				}
			}
		}()
	}
	for i := range pairs {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, run := range report.Runs {
		switch {
		case run.Status == StatusSkipped:
			report.Skipped++
		case run.Passed:
			report.Passed++
		default:
			report.Failed++
			if run.Status == StatusPartial {
				report.Partial++
			}
		}
	}
	report.DurationMs = time.Since(started).Milliseconds()
	report.Summary = summarize(report.Runs)
	return report, nil
}

func selectTask(task Task, options BenchOptions) bool {
	if options.TaskID != "" && task.ID != options.TaskID {
		return false
	}
	if len(options.TaskIDs) > 0 && !containsString(options.TaskIDs, task.ID) {
		return false
	}
	if options.SkipNetwork && task.Network {
		return false
	}
	if len(options.Tags) > 0 {
		hit := containsString(options.Tags, task.Category)
		for _, tag := range task.Tags {
			hit = hit || containsString(options.Tags, tag)
		}
		if !hit {
			return false
		}
	}
	return true
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if strings.TrimSpace(item) == s {
			return true
		}
	}
	return false
}

func runOne(parent context.Context, suiteDir string, options BenchOptions, task Task, model string, repeat int, ignore []string, runner AgentRunner) RunReport {
	started := time.Now()
	run := RunReport{TaskID: task.ID, Model: model, Repeat: repeat, Category: task.Category}
	name := sanitizeName(task.ID + "-" + model + "-" + fmt.Sprint(time.Now().UnixNano()))
	runDir := filepath.Join(options.WorkRoot, name)
	workspace := runDir
	dataDir := ""
	if options.NestWorkspace {
		workspace = filepath.Join(runDir, "workspace")
		dataDir = filepath.Join(runDir, "data")
	}
	if !options.KeepWorkspaces {
		defer os.RemoveAll(runDir)
	}
	fail := func(err error) RunReport {
		run.AgentError = err.Error()
		run.Status = StatusFail
		run.FailureCategory = FailAgentError
		run.DurationMs = time.Since(started).Milliseconds()
		return run
	}
	fixture := filepath.Join(suiteDir, filepath.FromSlash(task.WorkspaceFixture))
	if err := copyTree(fixture, workspace); err != nil {
		return fail(err)
	}
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return fail(err)
		}
	}
	before, err := snapshotTree(workspace)
	if err != nil {
		return fail(err)
	}
	var outsideBefore map[string][sha256.Size]byte
	if options.NestWorkspace {
		outsideBefore, _ = snapshotOutside(runDir)
	}
	timeout := options.Timeout
	if task.TimeoutSeconds > 0 {
		timeout = time.Duration(task.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	out, agentErr := runner(ctx, AgentInput{Workspace: workspace, Prompt: task.Prompt, TaskID: task.ID, Model: model, RunDir: runDir, DataDir: dataDir, Repeat: repeat, Task: task})
	run.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	if agentErr != nil {
		run.AgentError = agentErr.Error()
	}
	run.Extra = out.Extra
	run.Answer = out.Answer
	if run.Answer == "" {
		run.Answer = ExtractAnswer(out.Stdout)
	}
	run.Answer = tail(run.Answer, 8192)
	run.StdoutTail = tail(out.Stdout, 4096)
	run.StderrTail = tail(out.Stderr, 4096)
	after, snapErr := snapshotTree(workspace)
	if snapErr != nil && run.AgentError == "" {
		run.AgentError = snapErr.Error()
	}
	var changed []string
	for _, file := range changedFiles(before, after) {
		if !matchAny(ignore, file) {
			changed = append(changed, file)
		}
	}
	run.ChangedFiles = changed
	run.MissingChanges = missingExpected(task.ExpectedChangedFiles, run.ChangedFiles)
	for _, file := range run.ChangedFiles {
		if matchAny(task.ForbiddenChangedFiles, file) {
			run.ForbiddenChanges = append(run.ForbiddenChanges, file)
		} else if task.StrictDiff && !matchAny(task.AllowedChangedFiles, file) {
			run.DisallowedChanges = append(run.DisallowedChanges, file)
		}
	}
	if options.NestWorkspace {
		outsideAfter, _ := snapshotOutside(runDir)
		run.OutsideWrites = changedFiles(outsideBefore, outsideAfter)
	}
	run.MissingTrace = missingTrace(task.RequiredTraceEvents, out.Stdout)
	run.Metrics = parseUsage(out.Stdout)
	if task.GraderOverlay != "" {
		if err := copyTree(filepath.Join(suiteDir, filepath.FromSlash(task.GraderOverlay)), workspace); err != nil && run.AgentError == "" {
			run.AgentError = "grader overlay: " + err.Error()
		}
	}
	checkTimeout := options.CheckTimeout
	if checkTimeout <= 0 {
		checkTimeout = 5 * time.Minute
	}
	for _, check := range task.VerificationCommands {
		checkCtx, checkCancel := context.WithTimeout(context.WithoutCancel(parent), checkTimeout)
		run.Checks = append(run.Checks, runCheck(checkCtx, workspace, check))
		checkCancel()
	}
	for _, check := range task.FileChecks {
		run.Checks = append(run.Checks, gradeFile(workspace, fixture, check))
	}
	for _, check := range task.AnswerChecks {
		run.Checks = append(run.Checks, gradeAnswer(run.Answer, check))
	}
	scoreRun(&run, task, run.TimedOut)
	run.DurationMs = time.Since(started).Milliseconds()
	return run
}

// snapshotOutside hashes files in runDir outside workspace/ and data/.
func snapshotOutside(runDir string) (map[string][sha256.Size]byte, error) {
	out := map[string][sha256.Size]byte{}
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		if entry.Name() == "workspace" || entry.Name() == "data" {
			continue
		}
		path := filepath.Join(runDir, entry.Name())
		if !entry.IsDir() {
			if data, err := os.ReadFile(path); err == nil {
				out[entry.Name()] = sha256.Sum256(data)
			}
			continue
		}
		sub, err := snapshotTree(path)
		if err != nil {
			continue
		}
		for rel, hash := range sub {
			out[entry.Name()+"/"+rel] = hash
		}
	}
	return out, nil
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
		childproc.HideWindow(cmd)
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
	childproc.HideWindow(cmd)
	cmd.Dir = workspace
	if len(check.Env) > 0 {
		cmd.Env = append(os.Environ(), check.Env...)
	}
	out, err := cmd.CombinedOutput()
	report := CheckReport{ID: check.ID, Kind: "command", Passed: err == nil, Weight: weightOr(check.Weight), Critical: !check.Optional, DurationMs: time.Since(started).Milliseconds(), OutputTail: tail(string(out), 4096)}
	if err == nil && check.ExpectOutput != nil && normalizeText(string(out)) != normalizeText(*check.ExpectOutput) {
		report.Passed = false
		report.Detail = fmt.Sprintf("output %q, want %q", clip(normalizeText(string(out)), 200), *check.ExpectOutput)
	}
	report = finishScore(report)
	if m := scoreLineRe.FindStringSubmatch(string(out)); m != nil && !report.Passed {
		// A grader script may report partial credit as EVAL_SCORE=<0..1>.
		var v float64
		if _, perr := fmt.Sscanf(m[1], "%g", &v); perr == nil && v >= 0 && v <= 1 {
			report.Score = v
		}
	}
	if err != nil {
		report.ExitCode = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			report.ExitCode = ee.ExitCode()
		}
	}
	return report
}

var scoreLineRe = regexp.MustCompile(`(?m)^EVAL_SCORE=([0-9.]+)\s*$`)

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
