package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/agenteval"
)

type stringsFlag []string

func (s *stringsFlag) String() string { return strings.Join(*s, ",") }
func (s *stringsFlag) Set(value string) error {
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			*s = append(*s, item)
		}
	}
	return nil
}

func main() {
	fs := flag.NewFlagSet("supercli-eval", flag.ExitOnError)
	suitePath := fs.String("suite", filepath.FromSlash("test/go/eval/suite.json"), "suite JSON path")
	workRoot := fs.String("work-root", filepath.Join(os.TempDir(), "supercli-eval"), "isolated workspace root")
	taskID := fs.String("task", "", "run only one task id")
	timeout := fs.Duration("timeout", 10*time.Minute, "timeout per task/model")
	keep := fs.Bool("keep-workspaces", false, "retain isolated workspaces")
	validate := fs.Bool("validate", false, "validate suite without running an agent")
	output := fs.String("output", "", "write JSON report")
	var models stringsFlag
	fs.Var(&models, "model", "model id (repeat or comma-separate for a matrix)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: supercli-eval [flags] -- <agent argv with {workspace} {prompt} {task_id} {model}>")
		fs.PrintDefaults()
	}
	_ = fs.Parse(os.Args[1:])
	if *validate {
		suite, err := agenteval.LoadSuite(*suitePath)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("eval suite %s: %d tasks valid\n", suite.ID, len(suite.Tasks))
		return
	}
	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	report, err := agenteval.Bench(context.Background(), agenteval.BenchOptions{SuitePath: *suitePath, WorkRoot: *workRoot, Models: models, TaskID: *taskID, AgentCommand: fs.Args(), Timeout: *timeout, KeepWorkspaces: *keep})
	if err != nil {
		fatal(err)
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			fatal(err)
		}
		if err := os.WriteFile(*output, append(data, '\n'), 0o644); err != nil {
			fatal(err)
		}
	}
	fmt.Println(string(data))
	if report.Failed > 0 {
		os.Exit(1)
	}
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "supercli-eval:", err); os.Exit(1) }
