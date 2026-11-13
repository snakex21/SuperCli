// Command supercli-evalsuite runs the SuperCli agent suite end to end:
// it builds (or takes) a supercli binary, runs every task in a fresh
// fixture copy with an isolated --data-dir (never the user's config),
// grades the result with internal/agenteval and writes report.json,
// report.md, per-run logs and readable transcripts.
//
//	go run ./cmd/supercli-evalsuite -provider openai -base-url http://127.0.0.1:1234/v1 \
//	    -model qwen3.6-35b-a3b -repeats 2 -out .tmp/evals/qwen
//
// The API key is read from the environment variable named by -key-env
// and handed to the child as SUPERCLI_LLM_API_KEY; it is never written
// to logs, reports or config files.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"supercli/internal/agenteval"
)

type options struct {
	suite, bin, provider, model, baseURL, keyEnv string
	tasks, tags, out, workRoot, agent, refDir    string
	label, ledger, agentConfig                   string
	repeats, jobs                                int
	timeout                                      time.Duration
	keep, skipNetwork                            bool
	budgetStop                                   float64
	priceIn, priceCached, priceOut               float64
}

func main() {
	var o options
	fs := flag.NewFlagSet("supercli-evalsuite", flag.ExitOnError)
	fs.StringVar(&o.suite, "suite", filepath.FromSlash("test/go/eval/agent-suite.json"), "suite JSON")
	fs.StringVar(&o.bin, "bin", "", "supercli binary (default: build ./cmd/supercli into the work root)")
	fs.StringVar(&o.provider, "provider", "openai", "supercli --provider")
	fs.StringVar(&o.model, "model", "", "supercli --model")
	fs.StringVar(&o.baseURL, "base-url", "", "supercli --base-url (e.g. http://127.0.0.1:1234/v1 for LM Studio)")
	fs.StringVar(&o.keyEnv, "key-env", "SUPERCLI_LLM_API_KEY", "environment variable holding the API key (passed to the child, never logged)")
	fs.StringVar(&o.tasks, "tasks", "", "comma-separated task ids (default: all)")
	fs.StringVar(&o.tags, "tags", "", "comma-separated tags or categories")
	fs.BoolVar(&o.skipNetwork, "skip-network", false, "skip tasks that need internet")
	fs.IntVar(&o.repeats, "repeats", 1, "runs per task (variance)")
	fs.IntVar(&o.jobs, "jobs", 1, "parallel runs (keep 1 for a local server)")
	fs.DurationVar(&o.timeout, "timeout", 15*time.Minute, "default agent timeout per run (tasks may override)")
	fs.StringVar(&o.out, "out", "", "output directory for report.json, report.md, logs/ and transcripts/ (required)")
	fs.StringVar(&o.workRoot, "work-root", "", "where run workspaces live (default: <temp>/supercli-evalsuite)")
	fs.BoolVar(&o.keep, "keep", false, "keep run workspaces")
	fs.StringVar(&o.agent, "agent", "supercli", "supercli | reference (apply known-good solutions; grader self-test) | noop (untouched fixture)")
	fs.StringVar(&o.refDir, "ref-dir", filepath.FromSlash("test/go/eval/agent-suite-ref"), "reference solutions for -agent reference")
	fs.StringVar(&o.label, "label", "", "label printed in the report (default: model)")
	fs.StringVar(&o.agentConfig, "agent-config", "", "TOML lines added to every isolated config (task agentConfig keys win), e.g. 'context_window = 32768'")
	fs.StringVar(&o.ledger, "ledger", "", "append per-run cost rows to this markdown ledger")
	fs.Float64Var(&o.budgetStop, "budget-stop", 0, "stop starting runs once the cumulative cost reaches this many USD (0 = no limit)")
	fs.Float64Var(&o.priceIn, "price-in", 0, "USD per 1M uncached input tokens")
	fs.Float64Var(&o.priceCached, "price-cached", 0, "USD per 1M cached input tokens")
	fs.Float64Var(&o.priceOut, "price-out", 0, "USD per 1M output tokens (reasoning included)")
	_ = fs.Parse(os.Args[1:])
	if o.out == "" {
		fatal(fmt.Errorf("-out is required"))
	}
	if o.agent == "supercli" && o.model == "" {
		fatal(fmt.Errorf("-model is required"))
	}
	if o.workRoot == "" {
		o.workRoot = filepath.Join(os.TempDir(), "supercli-evalsuite", time.Now().Format("20060102-150405"))
	}
	for _, dir := range []string{o.out, filepath.Join(o.out, "logs"), filepath.Join(o.out, "transcripts"), o.workRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fatal(err)
		}
	}
	if o.label == "" {
		o.label = o.model
	}
	var runner agenteval.AgentRunner
	switch o.agent {
	case "supercli":
		if o.bin == "" {
			o.bin = filepath.Join(o.workRoot, "supercli"+exeSuffix())
			build := exec.Command("go", "build", "-o", o.bin, "./cmd/supercli")
			build.Stdout, build.Stderr = os.Stderr, os.Stderr
			if err := build.Run(); err != nil {
				fatal(fmt.Errorf("build supercli: %w", err))
			}
		}
		abs, err := filepath.Abs(o.bin)
		if err != nil {
			fatal(err)
		}
		o.bin = abs
		runner = superCLIRunner(o)
	case "reference":
		runner = referenceRunner(o.refDir)
	case "noop":
		runner = func(context.Context, agenteval.AgentInput) (agenteval.AgentOutput, error) {
			return agenteval.AgentOutput{Answer: "(noop)"}, nil
		}
	default:
		fatal(fmt.Errorf("unknown -agent %q", o.agent))
	}

	ledger := newLedger(o)
	started := time.Now()
	var mu sync.Mutex
	done := 0
	report, err := agenteval.Bench(context.Background(), agenteval.BenchOptions{
		SuitePath:      o.suite,
		WorkRoot:       o.workRoot,
		Models:         []string{o.model},
		Timeout:        o.timeout,
		KeepWorkspaces: o.keep,
		Runner:         runner,
		Jobs:           o.jobs,
		Repeats:        o.repeats,
		TaskIDs:        splitList(o.tasks),
		Tags:           splitList(o.tags),
		SkipNetwork:    o.skipNetwork,
		NestWorkspace:  true,
		OnRunStart: func(task agenteval.Task, _ string, repeat int) {
			fmt.Fprintf(os.Stderr, "[%s] start %s r%d\n", time.Now().Format("15:04:05"), task.ID, repeat)
		},
		OnRunDone: func(run agenteval.RunReport) {
			mu.Lock()
			defer mu.Unlock()
			done++
			cost := ledger.add(run)
			tokens := ""
			if m := run.Metrics; m != nil {
				tokens = fmt.Sprintf(" tools=%d in=%d cached=%d out=%d", m.ToolCalls, m.Input, m.Cached, m.Output)
			}
			fmt.Fprintf(os.Stderr, "[%s] done  %s r%d: %s score=%.2f %s%s %.0fs cost=$%.4f total=$%.4f\n",
				time.Now().Format("15:04:05"), run.TaskID, run.Repeat, run.Status, run.Score, run.FailureCategory, tokens,
				float64(run.DurationMs)/1000, cost, ledger.total)
			writeJSON(filepath.Join(o.out, "partial.json"), run, true)
		},
		ShouldStop: func() bool {
			mu.Lock()
			defer mu.Unlock()
			return o.budgetStop > 0 && ledger.total >= o.budgetStop
		},
	})
	if err != nil {
		fatal(err)
	}
	suite, _ := agenteval.LoadSuite(o.suite)
	full := fullReport{Label: o.label, Provider: o.provider, Model: o.model, Agent: o.agent, Platform: agenteval.Platform(),
		Repeats: o.repeats, CostUSD: ledger.total, WallMs: time.Since(started).Milliseconds(), Report: report}
	writeJSON(filepath.Join(o.out, "report.json"), full, false)
	md := renderMarkdown(full, suite)
	if err := os.WriteFile(filepath.Join(o.out, "report.md"), []byte(md), 0o644); err != nil {
		fatal(err)
	}
	fmt.Println(md)
}

type fullReport struct {
	Label    string  `json:"label"`
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	Agent    string  `json:"agent"`
	Platform string  `json:"platform"`
	Repeats  int     `json:"repeats"`
	CostUSD  float64 `json:"costUsd"`
	WallMs   int64   `json:"wallMs"`
	agenteval.Report
}

func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func writeJSON(path string, v any, appendLine bool) {
	if appendLine {
		data, _ := json.Marshal(v)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.Write(append(data, '\n'))
		return
	}
	data, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		fatal(err)
	}
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "supercli-evalsuite:", err)
	os.Exit(1)
}
