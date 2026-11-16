// Command ho3runner is a drop-in `-bin` for supercli-evalsuite that adds
// what the held-out suite "ho3" needs on top of a single `supercli --batch`
// call (see test/go/eval/agent-suite-ho3-gen/README.md):
//
//   - Workspace setup: when the workspace contains .ho3setup/setup.py, that
//     directory is moved out of the workspace and erased from the fixture
//     commit, then setup.py runs (python, cwd = workspace) to build git
//     history, branches or other state the agent must not see being made.
//   - Multi-turn conversations: the prompt may hold several user turns
//     separated by a line `>>> NEXT USER TURN <<<`. Every turn runs the real
//     binary once with the same flags (each --batch run is a new session);
//     turn k>1 receives the earlier user turns and the agent's earlier
//     answers, labelled as the earlier part of this conversation. One
//     synthetic `{"type":"usage","name":"run"}` line with the summed token
//     counters is printed last, so evalsuite's cost ledger stays correct.
//   - Reference self-test: with HO3_REFERENCE_DIR set, <dir>/<task>/solve.py
//     runs after setup instead of supercli and its stdout is the answer.
//   - Noop self-test: with HO3_MODE=noop, only the setup runs.
//
// It is invoked exactly like supercli:
//
//	ho3runner --data-dir D --home W --provider P --model M [--base-url U] --batch PROMPT --trace-jsonl
//
// with cwd = workspace. The real binary comes from HO3_SUPERCLI_BIN.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// TurnMarker separates user turns inside one task prompt.
const TurnMarker = ">>> NEXT USER TURN <<<"

const setupDir = ".ho3setup"

func main() {
	os.Exit(run())
}

func run() int {
	args := os.Args[1:]
	prompt, hasPrompt := flagValue(args, "--batch")
	dataDir, _ := flagValue(args, "--data-dir")
	if !hasPrompt {
		fmt.Fprintln(os.Stderr, "ho3runner: --batch PROMPT is required")
		return 2
	}
	ws, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ho3runner:", err)
		return 2
	}
	stop := installInterrupt()
	defer stop()

	if err := runSetup(ws); err != nil {
		fmt.Fprintln(os.Stderr, "ho3runner: setup:", err)
		return 3
	}

	if refDir := os.Getenv("HO3_REFERENCE_DIR"); refDir != "" {
		return runReference(refDir, dataDir, ws)
	}
	if strings.EqualFold(os.Getenv("HO3_MODE"), "noop") {
		fmt.Println("(noop)")
		printUsage(map[string]any{"type": "usage", "name": "run", "model": "noop"})
		return 0
	}
	bin := os.Getenv("HO3_SUPERCLI_BIN")
	if bin == "" {
		fmt.Fprintln(os.Stderr, "ho3runner: HO3_SUPERCLI_BIN is not set")
		return 2
	}
	return runTurns(bin, args, prompt, ws)
}

// flagValue returns the value after name (also accepts name=value).
func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"="), true
		}
	}
	return "", false
}

// withFlag returns args with the value after name replaced.
func withFlag(args []string, name, value string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		if a == name && i+1 < len(out) {
			out[i+1] = value
			return out
		}
		if strings.HasPrefix(a, name+"=") {
			out[i] = name + "=" + value
			return out
		}
	}
	return out
}

// ---------------------------------------------------------------- children

var (
	childMu sync.Mutex
	child   *os.Process
)

// installInterrupt forwards Ctrl+C / termination to the running child, so
// the wrapper never leaves an orphan behind. (evalsuite's timeout path uses
// taskkill /T /F, which already covers the whole process tree.)
func installInterrupt() func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			childMu.Lock()
			if child != nil {
				_ = child.Kill()
			}
			childMu.Unlock()
			os.Exit(130)
		case <-done:
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}

func runChild(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	childMu.Lock()
	child = cmd.Process
	childMu.Unlock()
	err := cmd.Wait()
	childMu.Lock()
	child = nil
	childMu.Unlock()
	return err
}

func python() string {
	if p := os.Getenv("HO3_PYTHON"); p != "" {
		return p
	}
	return "python"
}

// ---------------------------------------------------------------- setup

func git(ws string, args ...string) ([]byte, error) {
	full := append([]string{"-c", "user.name=evalsuite", "-c", "user.email=evalsuite@localhost",
		"-c", "core.autocrlf=false", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = ws
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := runChild(cmd)
	if err != nil {
		return out.Bytes(), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.Bytes(), nil
}

// runSetup moves .ho3setup out of the workspace, removes it from the
// fixture commit evalsuite made, and runs setup.py against the workspace.
func runSetup(ws string) error {
	if _, err := os.Stat(filepath.Join(ws, ".git")); err == nil {
		// Graders compare exact bytes; never let the host's global
		// core.autocrlf rewrite files when the agent runs git checkout.
		if _, err := git(ws, "config", "core.autocrlf", "false"); err != nil {
			return err
		}
	}
	src := filepath.Join(ws, setupDir)
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return nil
	}
	tmp, err := os.MkdirTemp("", "ho3setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	dst := filepath.Join(tmp, setupDir)
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(ws, ".git")); err == nil {
		// Erase the setup files from the fixture commit and from every
		// reflog, so the agent cannot find them in history.
		if _, err := git(ws, "rm", "-r", "-q", "--cached", "--ignore-unmatch", setupDir); err != nil {
			return err
		}
		if _, err := git(ws, "commit", "-q", "--amend", "--no-edit", "--allow-empty"); err != nil {
			return err
		}
		if _, err := git(ws, "reflog", "expire", "--expire=now", "--all"); err != nil {
			return err
		}
		if _, err := git(ws, "gc", "-q", "--prune=now"); err != nil {
			return err
		}
	}
	script := filepath.Join(dst, "setup.py")
	if _, err := os.Stat(script); err != nil {
		return nil
	}
	cmd := exec.Command(python(), script)
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "HO3_SETUP_DIR="+dst)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := runChild(cmd); err != nil {
		return fmt.Errorf("setup.py: %v: %s", err, strings.TrimSpace(out.String()))
	}
	return nil
}

// ---------------------------------------------------------------- reference

var runSuffix = regexp.MustCompile(`-r\d+-\d+$`)

// taskIDFromDataDir maps <workroot>/<sanitized task-model-rN-nanos>/data to
// the task id: the longest reference directory name that prefixes the run
// directory name (robust to sanitizeName's 100-char truncation).
func taskIDFromDataDir(refDir, dataDir string) (string, error) {
	if dataDir == "" {
		return "", errors.New("no --data-dir")
	}
	runName := filepath.Base(filepath.Dir(filepath.Clean(dataDir)))
	entries, err := os.ReadDir(refDir)
	if err != nil {
		return "", err
	}
	best := ""
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(runName, n+"-") && len(n) > len(best) {
			if _, err := os.Stat(filepath.Join(refDir, n, "solve.py")); err == nil {
				best = n
			}
		}
	}
	if best == "" {
		return "", fmt.Errorf("no reference solve.py matches run %q", runSuffix.ReplaceAllString(runName, ""))
	}
	return best, nil
}

func runReference(refDir, dataDir, ws string) int {
	id, err := taskIDFromDataDir(refDir, dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ho3runner: reference:", err)
		return 3
	}
	script, _ := filepath.Abs(filepath.Join(refDir, id, "solve.py"))
	cmd := exec.Command(python(), script)
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	var stdout bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, os.Stderr
	err = runChild(cmd)
	os.Stdout.Write(stdout.Bytes())
	if stdout.Len() > 0 && !bytes.HasSuffix(stdout.Bytes(), []byte("\n")) {
		fmt.Println()
	}
	printUsage(map[string]any{"type": "usage", "name": "run", "model": "reference"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "ho3runner: solve.py:", err)
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- turns

// SplitTurns splits a prompt on lines equal to TurnMarker.
func SplitTurns(prompt string) []string {
	var turns []string
	var cur []string
	for _, line := range strings.Split(strings.ReplaceAll(prompt, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == TurnMarker {
			turns = append(turns, strings.TrimSpace(strings.Join(cur, "\n")))
			cur = nil
			continue
		}
		cur = append(cur, line)
	}
	turns = append(turns, strings.TrimSpace(strings.Join(cur, "\n")))
	out := turns[:0]
	for _, t := range turns {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ComposeTurn builds the prompt for turn k (0-based) from the earlier user
// turns and the agent's earlier answers.
func ComposeTurn(users, answers []string, k int) string {
	if k == 0 {
		return users[0]
	}
	var b strings.Builder
	b.WriteString("This message continues an earlier conversation with you in this same session. ")
	b.WriteString("The earlier part of the conversation is quoted below for context. ")
	b.WriteString("The workspace already contains every change you made during it; inspect the files instead of assuming.\n\n")
	b.WriteString("=== EARLIER CONVERSATION (already handled) ===\n")
	for i := 0; i < k; i++ {
		fmt.Fprintf(&b, "\n--- User (turn %d) ---\n%s\n", i+1, users[i])
		ans := strings.TrimSpace(answers[i])
		if ans == "" {
			ans = "(no answer text)"
		}
		fmt.Fprintf(&b, "\n--- You, final answer (turn %d) ---\n%s\n", i+1, ans)
	}
	b.WriteString("\n=== END OF EARLIER CONVERSATION ===\n\n")
	fmt.Fprintf(&b, "--- User (turn %d), the new message to act on now ---\n%s\n", k+1, users[k])
	return b.String()
}

var thinkingRe = regexp.MustCompile(`(?s)<thinking>.*?</thinking>`)

// answerFromStdout mirrors agenteval.ExtractAnswer and keeps the tail.
func answerFromStdout(stdout string) string {
	stdout = thinkingRe.ReplaceAllString(stdout, "")
	var b strings.Builder
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), `{"type":`) {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	s := strings.TrimSpace(b.String())
	const max = 6000
	if len(s) > max {
		s = "[... earlier text omitted ...]\n" + s[len(s)-max:]
	}
	return s
}

// lastUsage returns the last {"type":"usage","name":"run"} object.
func lastUsage(stdout string) map[string]any {
	var last map[string]any
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if m["type"] == "usage" && m["name"] == "run" {
			last = m
		}
	}
	return last
}

// SumUsage adds the numeric top-level fields of every turn's usage line.
// ttftMs is the first turn's value and ttftAvgMs the mean over turns that
// reported one; error is true when any turn reported an error; nested
// objects (review, context) are dropped.
func SumUsage(lines []map[string]any) map[string]any {
	out := map[string]any{"type": "usage", "name": "run"}
	var ttftAvg float64
	ttftN := 0
	for i, m := range lines {
		for k, v := range m {
			switch k {
			case "type", "name":
				continue
			case "ttftMs":
				if f, ok := v.(float64); ok {
					if _, set := out[k]; !set || i == 0 {
						out[k] = f
					}
				}
				continue
			case "ttftAvgMs":
				if f, ok := v.(float64); ok && f > 0 {
					ttftAvg += f
					ttftN++
				}
				continue
			}
			switch x := v.(type) {
			case float64:
				prev, _ := out[k].(float64)
				out[k] = prev + x
			case bool:
				prev, _ := out[k].(bool)
				out[k] = prev || x
			case string:
				out[k] = x
			}
		}
	}
	if ttftN > 0 {
		out["ttftAvgMs"] = ttftAvg / float64(ttftN)
	}
	out["turns"] = float64(len(lines))
	return out
}

func printUsage(m map[string]any) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// type and name first keep the line recognisable by prefix matchers.
	var b strings.Builder
	b.WriteString(`{"type":"usage","name":"run"`)
	for _, k := range keys {
		if k == "type" || k == "name" {
			continue
		}
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(m[k])
		b.WriteString("," + string(kb) + ":" + string(vb))
	}
	b.WriteString("}\n")
	os.Stdout.WriteString(b.String())
}

func runTurns(bin string, args []string, prompt, ws string) int {
	users := SplitTurns(prompt)
	if len(users) == 0 {
		users = []string{prompt}
	}
	answers := make([]string, len(users))
	var usages []map[string]any
	failed := false
	for k := range users {
		turnArgs := withFlag(args, "--batch", ComposeTurn(users, answers, k))
		cmd := exec.Command(bin, turnArgs...)
		cmd.Dir = ws
		var stdout bytes.Buffer
		cmd.Stdout = io.MultiWriter(&stdout, os.Stdout)
		cmd.Stderr = os.Stderr
		if len(users) > 1 {
			fmt.Fprintf(os.Stderr, "[ho3runner] turn %d/%d start %s\n", k+1, len(users), time.Now().Format("15:04:05"))
		}
		err := runChild(cmd)
		if !bytes.HasSuffix(stdout.Bytes(), []byte("\n")) && stdout.Len() > 0 {
			fmt.Println()
		}
		if err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "[ho3runner] turn %d failed: %v\n", k+1, err)
		}
		answers[k] = answerFromStdout(stdout.String())
		if u := lastUsage(stdout.String()); u != nil {
			usages = append(usages, u)
		}
	}
	if len(usages) > 0 {
		sum := SumUsage(usages)
		if failed {
			sum["error"] = true
		}
		printUsage(sum)
	}
	if failed {
		return 1
	}
	return 0
}
