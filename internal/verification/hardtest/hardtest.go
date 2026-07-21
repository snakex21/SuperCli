// Package hardtest runs the repository's strongest deterministic checks without
// involving an LLM. It is deliberately opt-in because race and lint passes can
// be expensive on large projects.
package hardtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"supercli/internal/system/childproc"
	"supercli/internal/tools/core"
)

type Check struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output,omitempty"`
}
type Report struct {
	OK         bool    `json:"ok"`
	Checks     []Check `json:"checks"`
	DurationMS int64   `json:"duration_ms"`
}
type command struct {
	name, bin string
	args      []string
}

func Detect(root string) []command {
	out := []command{}
	if exists(filepath.Join(root, "go.mod")) {
		out = append(out, command{"Go tests", "go", []string{"test", "./..."}}, command{"Go vet", "go", []string{"vet", "./..."}}, command{"Race detector", "go", []string{"test", "-race", "./internal/..."}})
	}
	if b, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		var p struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(b, &p) == nil {
			npm := "npm"
			if runtime.GOOS == "windows" {
				npm = "npm.cmd"
			}
			for _, n := range []string{"test", "lint", "typecheck"} {
				if p.Scripts[n] != "" {
					out = append(out, command{"npm " + n, npm, []string{"run", n}})
				}
			}
		}
	}
	if exists(filepath.Join(root, "Cargo.toml")) {
		out = append(out, command{"Cargo tests", "cargo", []string{"test", "--all-targets"}}, command{"Clippy", "cargo", []string{"clippy", "--all-targets", "--", "-D", "warnings"}})
	}
	if exists(filepath.Join(root, "pyproject.toml")) || exists(filepath.Join(root, "pytest.ini")) || exists(filepath.Join(root, "setup.cfg")) {
		out = append(out, command{"Python tests", pythonBin(), []string{"-m", "pytest"}})
	}
	return out
}

func Run(ctx context.Context, root string) (Report, error) {
	cmds := Detect(root)
	if len(cmds) == 0 {
		return Report{}, fmt.Errorf("test hard: no supported test manifest found")
	}
	start := time.Now()
	report := Report{OK: true, Checks: make([]Check, 0, len(cmds))}
	for _, c := range cmds {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		beg := time.Now()
		var buf bytes.Buffer
		cmd := exec.CommandContext(ctx, c.bin, c.args...)
		childproc.HideWindow(cmd)
		cmd.Dir = root
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		ok := err == nil
		out := strings.TrimSpace(core.HeadTail(buf.String(), 48*1024, 16*1024))
		if err != nil && out == "" {
			out = err.Error()
		}
		report.Checks = append(report.Checks, Check{Name: c.name, Command: c.bin + " " + strings.Join(c.args, " "), OK: ok, DurationMS: time.Since(beg).Milliseconds(), Output: out})
		if !ok {
			report.OK = false
		}
	}
	report.DurationMS = time.Since(start).Milliseconds()
	return report, nil
}

func Markdown(r Report) string {
	var b strings.Builder
	if r.OK {
		b.WriteString("test hard: PASS")
	} else {
		b.WriteString("test hard: FAIL")
	}
	fmt.Fprintf(&b, " (%s)\n", time.Duration(r.DurationMS)*time.Millisecond)
	for _, c := range r.Checks {
		mark := "PASS"
		if !c.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "- %s  %s  %s\n", mark, c.Name, time.Duration(c.DurationMS)*time.Millisecond)
		if !c.OK && c.Output != "" {
			b.WriteString("\n```text\n" + c.Output + "\n```\n")
		}
	}
	return b.String()
}
func exists(p string) bool { _, err := os.Stat(p); return err == nil }
func pythonBin() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}
