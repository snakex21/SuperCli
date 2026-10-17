// Command ui-checks runs opt-in native browser checks, never a user GUI/model.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"supercli/internal/testbrowser"
)

type options struct {
	Case, URL, NestCafeURL, Mode, CallID, Browser, Root, Out string
	Timeout                                                  time.Duration
}

type report struct {
	Case        string         `json:"case"`
	Passed      bool           `json:"passed"`
	Checks      []string       `json:"checks"`
	Metrics     map[string]any `json:"metrics"`
	Screenshots []string       `json:"screenshots"`
	Artifacts   string         `json:"artifacts"`
	Limitations []string       `json:"limitations,omitempty"`
}

var caseNames = []string{"delegation-ui", "model-palette", "reasoning-ui", "session-activity", "ui-scaling", "chat-command-end-http", "chat-stop-http"}

func validate(opts options) error {
	found := false
	for _, name := range caseNames {
		if opts.Case == name {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("-case must be one of %s", strings.Join(caseNames, "|"))
	}
	needsURL := opts.Case == "model-palette" || opts.Case == "ui-scaling" || strings.HasPrefix(opts.Case, "chat-")
	if needsURL && opts.URL == "" {
		return fmt.Errorf("%s requires an explicit -url for an owned test server; no GUI or model is started", opts.Case)
	}
	if !needsURL && (opts.URL != "" || opts.NestCafeURL != "") {
		return errors.New("fixture-backed cases do not accept external URLs")
	}
	if opts.NestCafeURL != "" && opts.Case != "ui-scaling" {
		return errors.New("-nestcafe-url is only valid with ui-scaling")
	}
	for _, raw := range []string{opts.URL, opts.NestCafeURL} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return fmt.Errorf("invalid owned server origin %q", raw)
		}
		if strings.HasPrefix(opts.Case, "chat-") && (u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "") {
			return errors.New("chat checks require an explicit owned http://127.0.0.1:PORT fixture origin")
		}
	}
	if opts.Case == "chat-command-end-http" {
		if !contains([]string{"cmd-if-exist", "curl-gif", "cmd-error", "checkpoint-rejected"}, opts.Mode) {
			return errors.New("-mode must be cmd-if-exist|curl-gif|cmd-error|checkpoint-rejected")
		}
		if opts.CallID != "command-end-"+opts.Mode {
			return errors.New("-call-id must equal command-end-MODE")
		}
	} else if opts.Case == "chat-stop-http" {
		if !contains([]string{"running-command", "checkpoint-store-held"}, opts.Mode) {
			return errors.New("-mode must be running-command|checkpoint-store-held")
		}
	} else if opts.Mode != "" || opts.CallID != "" {
		return errors.New("-mode/-call-id are only for chat fixture checks")
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func repoRoot(root string) (string, error) {
	if root != "" {
		return filepath.Abs(root)
	}
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(current, "internal", "webgui", "assets", "index.html")); err == nil && info.Mode().IsRegular() {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("run from the SuperCli checkout or pass -root")
		}
		current = parent
	}
}

type runState struct {
	ctx     context.Context
	opts    options
	browser *testbrowser.Browser
	page    *testbrowser.Page
	report  report
}

func (r *runState) phase(module, phase string, args any) error {
	encoded, err := json.Marshal(args)
	if err != nil {
		return err
	}
	expression := "import('/__ui_checks/" + module + ".js').then(m => m." + phase + "(" + string(encoded) + "))"
	if err := r.page.Eval(r.ctx, expression, nil); err != nil {
		return fmt.Errorf("%s/%s: %w", module, phase, err)
	}
	return nil
}

func (r *runState) screenshot(name string) error {
	data, err := r.page.Screenshot(r.ctx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.report.Artifacts, name+".png"), data, 0600); err != nil {
		return err
	}
	r.report.Screenshots = append(r.report.Screenshots, name+".png")
	return nil
}

func (r *runState) collect() error {
	var values struct {
		Checks  []string       `json:"checks"`
		Metrics map[string]any `json:"metrics"`
	}
	if err := r.page.Eval(r.ctx, "({checks:window.__uiCheckResults||[],metrics:window.__uiCheckMetrics||{}})", &values); err != nil {
		return err
	}
	r.report.Checks = append(r.report.Checks, values.Checks...)
	for key, value := range values.Metrics {
		r.report.Metrics[key] = value
	}
	return nil
}

func runCheck(ctx context.Context, opts options) (result report, err error) {
	result.Case = opts.Case
	if err := validate(opts); err != nil {
		return result, err
	}
	root, err := repoRoot(opts.Root)
	if err != nil {
		return result, err
	}
	opts.Root = root
	if opts.Out != "" {
		if err := os.MkdirAll(opts.Out, 0700); err != nil {
			return result, err
		}
	}
	artifacts, err := os.MkdirTemp(opts.Out, "supercli-ui-checks-"+opts.Case+"-*")
	if err != nil {
		return result, err
	}
	r := &runState{ctx: ctx, opts: opts, report: report{Case: opts.Case, Artifacts: artifacts, Metrics: map[string]any{}}}
	defer func() {
		r.report.Passed = err == nil
		result = r.report
		data, marshalErr := json.MarshalIndent(result, "", "  ")
		if marshalErr == nil {
			marshalErr = os.WriteFile(filepath.Join(artifacts, "result.json"), append(data, '\n'), 0600)
		}
		err = errors.Join(err, marshalErr)
	}()
	r.browser, err = testbrowser.Start(ctx, testbrowser.Options{Executable: opts.Browser, Width: 1280, Height: 900})
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, r.browser.Close()) }()
	switch opts.Case {
	case "delegation-ui", "reasoning-ui", "session-activity":
		err = r.fixtureCheck()
	case "model-palette":
		err = r.paletteCheck()
	case "ui-scaling":
		err = r.scalingCheck()
	case "chat-command-end-http", "chat-stop-http":
		err = r.chatCheck()
	}
	if err != nil && r.page != nil {
		_ = r.collect()
		_ = r.screenshot("failure")
	}
	return result, err
}

func cli(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ui-checks", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var opts options
	flags.StringVar(&opts.Case, "case", "", "check: "+strings.Join(caseNames, "|"))
	flags.StringVar(&opts.URL, "url", "", "explicit owned test server origin (no default); ui-scaling WRITES settings: use a disposable test config, never the user's normal app")
	flags.StringVar(&opts.NestCafeURL, "nestcafe-url", "", "optional owned NestCafe origin; never starts or reads another project")
	flags.StringVar(&opts.Mode, "mode", "", "chat fixture scenario")
	flags.StringVar(&opts.CallID, "call-id", "", "command-end fixture tool call ID")
	flags.StringVar(&opts.Browser, "browser", "", "Chrome/Chromium/Edge executable; defaults to SUPERCLI_TEST_BROWSER/discovery")
	flags.StringVar(&opts.Root, "root", "", "optional SuperCli checkout root")
	flags.StringVar(&opts.Out, "out", "", "parent for a unique artifacts directory (default OS temp)")
	flags.DurationVar(&opts.Timeout, "timeout", 90*time.Second, "whole-check deadline")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || opts.Timeout <= 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments or invalid -timeout")
		return 2
	}
	if opts.Case == "chat-stop-http" && opts.Mode == "" {
		opts.Mode = "running-command"
	}
	if err := validate(opts); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	result, err := runCheck(ctx, opts)
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Fprintln(stdout, string(data))
	if result.Case == "chat-command-end-http" && err == nil {
		metrics, _ := json.Marshal(result.Metrics)
		fmt.Fprintln(stdout, "command-end-result="+string(metrics))
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(cli(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
