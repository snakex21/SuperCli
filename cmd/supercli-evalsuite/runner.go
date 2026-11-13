package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"supercli/internal/agenteval"
	"supercli/internal/system/childproc"
)

// superCLIRunner runs `supercli --batch --trace-jsonl` in the task
// workspace with an isolated data dir and records logs + transcript.
func superCLIRunner(o options) agenteval.AgentRunner {
	return func(ctx context.Context, in agenteval.AgentInput) (agenteval.AgentOutput, error) {
		if err := prepareWorkspace(in); err != nil {
			return agenteval.AgentOutput{}, err
		}
		args := []string{"--data-dir", in.DataDir, "--home", in.Workspace, "--provider", o.provider, "--model", o.model}
		if o.baseURL != "" {
			args = append(args, "--base-url", o.baseURL)
		}
		args = append(args, "--batch", in.Prompt, "--trace-jsonl")
		cmd := exec.CommandContext(ctx, o.bin, args...)
		childproc.HideWindow(cmd)
		cmd.Dir = in.Workspace
		cmd.Env = childEnv(o)
		cmd.Cancel = func() error { return killTree(cmd.Process) }
		cmd.WaitDelay = 15 * time.Second
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		started := time.Now()
		err := cmd.Run()
		wall := time.Since(started)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("agent timeout after %s: %w", wall.Round(time.Second), ctx.Err())
		}
		base := fmt.Sprintf("%s-r%d", in.TaskID, in.Repeat)
		_ = os.WriteFile(filepath.Join(o.out, "logs", base+".out"), []byte(stdout.String()), 0o644)
		_ = os.WriteFile(filepath.Join(o.out, "logs", base+".err"), []byte(stderr.String()), 0o644)
		tr := readTranscript(filepath.Join(in.DataDir, "sessions.db"), in.Workspace, in.RunDir)
		tr.Compactions = strings.Count(stderr.String(), "[compact]")
		md := tr.markdown(in)
		_ = os.WriteFile(filepath.Join(o.out, "transcripts", base+".md"), []byte(md), 0o644)
		extra := map[string]any{
			"wallMs":        wall.Milliseconds(),
			"toolHistogram": tr.Histogram,
			"toolErrors":    tr.ToolErrors,
			"compactions":   tr.Compactions,
			"messages":      tr.Messages,
			"transcript":    filepath.ToSlash(filepath.Join("transcripts", base+".md")),
		}
		if len(tr.OutsideRefs) > 0 {
			extra["outsidePathRefs"] = tr.OutsideRefs
		}
		if err != nil {
			extra["exitError"] = err.Error()
		}
		// A non-zero exit after a completed answer is still graded on
		// its effects; only a missing usage line counts as agent error.
		if err != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) && strings.Contains(stdout.String(), `{"type":"usage"`) {
			err = nil
		}
		return agenteval.AgentOutput{Stdout: stdout.String(), Stderr: stderr.String(), Answer: tr.FinalAnswer, Extra: extra}, err
	}
}

// prepareWorkspace writes the isolated agent config and turns the
// fixture into a one-commit git repository, as a real project would be.
func prepareWorkspace(in agenteval.AgentInput) error {
	cfg := "# supercli-evalsuite isolated config\n" + strings.TrimSpace(in.Task.AgentConfig) + "\n"
	if err := os.WriteFile(filepath.Join(in.DataDir, "config.toml"), []byte(cfg), 0o644); err != nil {
		return err
	}
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-c", "user.name=evalsuite", "-c", "user.email=evalsuite@localhost", "-c", "core.autocrlf=false"}, args...)...)
		childproc.HideWindow(cmd)
		cmd.Dir = in.Workspace
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %s: %v: %s", args[0], err, out)
		}
		return nil
	}
	if err := git("init", "-q"); err != nil {
		return err
	}
	if err := git("add", "-A"); err != nil {
		return err
	}
	return git("commit", "-q", "-m", "fixture", "--allow-empty")
}

func childEnv(o options) []string {
	key := os.Getenv(o.keyEnv)
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "SUPERCLI_") {
			continue // never inherit the user's instance overrides
		}
		env = append(env, kv)
	}
	if key == "" && o.baseURL != "" {
		key = "local" // LM Studio and similar accept any key
	}
	if key != "" {
		env = append(env, "SUPERCLI_LLM_API_KEY="+key)
	}
	return env
}

func killTree(p *os.Process) error {
	if p == nil {
		return nil
	}
	if runtime.GOOS == "windows" {
		kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid))
		childproc.HideWindow(kill)
		_ = kill.Run()
	}
	return p.Kill()
}

// referenceRunner applies <ref>/<task>/files over the workspace and
// returns <ref>/<task>/answer.md: a grader self-test that must pass.
func referenceRunner(refDir string) agenteval.AgentRunner {
	return func(_ context.Context, in agenteval.AgentInput) (agenteval.AgentOutput, error) {
		dir := filepath.Join(refDir, in.TaskID)
		files := filepath.Join(dir, "files")
		if info, err := os.Stat(files); err == nil && info.IsDir() {
			err := filepath.WalkDir(files, func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(files, p)
				data, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				target := filepath.Join(in.Workspace, rel)
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					return err
				}
				return os.WriteFile(target, data, 0o644)
			})
			if err != nil {
				return agenteval.AgentOutput{}, err
			}
		}
		answer, _ := os.ReadFile(filepath.Join(dir, "answer.md"))
		return agenteval.AgentOutput{Answer: string(answer), Stdout: string(answer)}, nil
	}
}

type transcript struct {
	Entries     []entry
	Histogram   map[string]int
	ToolErrors  int
	Compactions int
	Messages    int
	FinalAnswer string
	OutsideRefs []string
}

type entry struct {
	Role, Content, Name string
	Calls               []call
}

type call struct{ Name, Args string }

var toolErrorRe = regexp.MustCompile(`(?i)^\s*(error|failed|blocked|denied|refused|tool error|invalid)`)
var absPathRe = regexp.MustCompile(`(?i)[a-z]:[\\/]{1,2}[^"'\s<>|*?]+`)

func readTranscript(dbPath, workspace, runDir string) transcript {
	tr := transcript{Histogram: map[string]int{}}
	if _, err := os.Stat(dbPath); err != nil {
		return tr
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro")
	if err != nil {
		return tr
	}
	defer db.Close()
	rows, err := db.Query(`SELECT m.role, COALESCE(m.content,''), COALESCE(m.tool_calls_json,''), COALESCE(m.name,'')
		FROM messages m JOIN sessions s ON s.id = m.session_id
		ORDER BY s.created_at, m.session_id, m.seq`)
	if err != nil {
		return tr
	}
	defer rows.Close()
	outside := map[string]bool{}
	for rows.Next() {
		var e entry
		var callsJSON string
		if rows.Scan(&e.Role, &e.Content, &callsJSON, &e.Name) != nil {
			continue
		}
		tr.Messages++
		e.Calls = parseCalls(callsJSON)
		for _, c := range e.Calls {
			tr.Histogram[c.Name]++
			for _, p := range absPathRe.FindAllString(c.Args, -1) {
				p = strings.ReplaceAll(strings.TrimRight(p, `\/.,;:)`), `\\`, `\`)
				if !underDir(p, workspace) && !underDir(p, filepath.Join(runDir, "data")) {
					outside[p] = true
				}
			}
		}
		if e.Role == "tool" && toolErrorRe.MatchString(e.Content) {
			tr.ToolErrors++
		}
		if e.Role == "assistant" && strings.TrimSpace(e.Content) != "" {
			tr.FinalAnswer = e.Content
		}
		tr.Entries = append(tr.Entries, e)
	}
	for p := range outside {
		tr.OutsideRefs = append(tr.OutsideRefs, p)
	}
	sort.Strings(tr.OutsideRefs)
	if len(tr.OutsideRefs) > 10 {
		tr.OutsideRefs = tr.OutsideRefs[:10]
	}
	return tr
}

func underDir(p, dir string) bool {
	p = strings.ToLower(filepath.Clean(filepath.FromSlash(p)))
	dir = strings.ToLower(filepath.Clean(dir))
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

func parseCalls(raw string) []call {
	if strings.TrimSpace(raw) == "" || raw == "null" {
		return nil
	}
	var items []map[string]any
	if json.Unmarshal([]byte(raw), &items) != nil {
		return nil
	}
	var out []call
	for _, item := range items {
		c := call{}
		src := item
		if fn, ok := item["function"].(map[string]any); ok {
			src = fn
		}
		for _, k := range []string{"name", "Name"} {
			if v, ok := src[k].(string); ok && v != "" {
				c.Name = v
			}
		}
		for _, k := range []string{"arguments", "Arguments", "args", "input", "Input"} {
			switch v := src[k].(type) {
			case string:
				c.Args = v
			case nil:
			default:
				b, _ := json.Marshal(v)
				c.Args = string(b)
			}
			if c.Args != "" {
				break
			}
		}
		out = append(out, c)
	}
	return out
}

func (tr transcript) markdown(in agenteval.AgentInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (repeat %d)\n\nworkspace: `%s`\n\n## Prompt\n\n%s\n\n", in.TaskID, in.Repeat, in.Workspace, in.Prompt)
	fmt.Fprintf(&b, "messages=%d tool errors=%d compactions=%d tools=%v\n\n", tr.Messages, tr.ToolErrors, tr.Compactions, tr.Histogram)
	for i, e := range tr.Entries {
		switch e.Role {
		case "system":
			fmt.Fprintf(&b, "### %d system (%d chars)\n\n", i, len(e.Content))
			continue
		case "tool":
			fmt.Fprintf(&b, "### %d tool result %s\n\n```\n%s\n```\n\n", i, e.Name, clip(e.Content, 1500))
			continue
		}
		fmt.Fprintf(&b, "### %d %s\n\n", i, e.Role)
		if strings.TrimSpace(e.Content) != "" {
			fmt.Fprintf(&b, "%s\n\n", clip(e.Content, 4000))
		}
		for _, c := range e.Calls {
			fmt.Fprintf(&b, "- call `%s` %s\n", c.Name, "`"+strings.ReplaceAll(clip(c.Args, 2000), "`", "'")+"`")
		}
		if len(e.Calls) > 0 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("… [+%d chars]", len(s)-n)
}
