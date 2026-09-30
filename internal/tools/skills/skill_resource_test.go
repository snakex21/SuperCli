package skills

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/tools/core"
	"supercli/internal/tools/sandbox"
)

func resourceRegistry(t *testing.T, applier *SkillApplier) *core.Registry {
	t.Helper()
	reg := core.NewRegistry()
	reg.MustRegister(applier.Spec())
	return reg
}

func callResourceTool(t *testing.T, reg *core.Registry, args string) core.Result {
	t.Helper()
	result, err := reg.Execute(context.Background(), "apply_skill", json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestBuiltinSkillResourceOutsideWorkspaceWithoutReapplying(t *testing.T) {
	wasUnsandboxed := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(false)
	t.Cleanup(func() { sandbox.SetUnsandboxed(wasUnsandboxed) })
	root := t.TempDir()
	workspace, data := filepath.Join(root, "workspace"), filepath.Join(root, "data")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "skills"), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(data, "skills", "builtin-skills.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{
		"skills/007/SKILL.md":            "# Selected\nRead references/check.md before acting.",
		"skills/007/references/check.md": "REQUIRED CHECK\nSecond check.\n",
		"skills/007/scripts/helper.ps1":  "Write-Output 'readable script; never execute here'\n",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	d := &Discoverer{Builtin: NewBuiltinPack(data)}
	applier := NewSkillApplier(d)
	reg := resourceRegistry(t, applier)
	applied := callResourceTool(t, reg, `{"name":"007"}`)
	if applied.Err != nil || !strings.Contains(applied.Text, "<skill-guidance>") {
		t.Fatalf("apply: %+v", applied)
	}
	before := applier.AppendSkills()
	path := applier.details["007"].path
	resourcePath := filepath.Join(filepath.Dir(path), "references", "check.md")
	if _, err := sandbox.ResolveWithin(workspace, resourcePath); err == nil {
		t.Fatal("fixture resource unexpectedly inside workspace")
	}
	// A resource read must not reopen or require the selected guidance body.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	read := callResourceTool(t, reg, `{"name":"007","resource":"references/check.md","from":2,"to":2}`)
	if read.Err != nil || !strings.Contains(read.Text, "2 | Second check.") || strings.Contains(read.Text, "<skill-guidance>") {
		t.Fatalf("resource read: %+v", read)
	}
	if applier.AppendSkills() != before || len(applier.Applied()) != 1 {
		t.Fatal("resource read rewrote activation or guidance")
	}
	// Rebuilt appliers (resume/next WebGUI request) use metadata and the materialized
	// resource directory; missing SKILL.md still must not force another load.
	resumed := NewSkillApplier(d)
	reread := callResourceTool(t, resourceRegistry(t, resumed), `{"name":"007","resource":"references/check.md"}`)
	if reread.Err != nil || !strings.Contains(reread.Text, "REQUIRED CHECK") || len(resumed.Applied()) != 0 {
		t.Fatalf("rebuilt resource read: %+v", reread)
	}
	script := callResourceTool(t, reg, `{"name":"007","resource":"scripts/helper.ps1"}`)
	if script.Err != nil || !strings.Contains(script.Text, "Write-Output") || sandbox.IsUnsandboxed() {
		t.Fatalf("read-only script resource: %+v", script)
	}
	repeated := callResourceTool(t, reg, `{"name":"007"}`)
	if repeated.Err != nil || repeated.Text != applied.Text {
		t.Fatalf("normal apply changed: %+v", repeated)
	}
}

func TestSkillResourceRejectsEscapeAndBinary(t *testing.T) {
	wasUnsandboxed := sandbox.IsUnsandboxed()
	sandbox.SetUnsandboxed(true) // Resource boundaries are strict even in allow-all mode.
	t.Cleanup(func() { sandbox.SetUnsandboxed(wasUnsandboxed) })
	root := t.TempDir()
	skillsDir := filepath.Join(root, "skills")
	mkSkillDir(t, skillsDir, "alpha", "---\ndescription: resource fixture\n---\nselected guidance")
	dir := filepath.Join(skillsDir, "alpha")
	outside := filepath.Join(root, "outside.md")
	if err := os.WriteFile(outside, []byte("OUTSIDE MUST STAY HIDDEN"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "image.png"), []byte{0x89, 'P', 'N', 'G', 0, 0, 1}, 0600); err != nil {
		t.Fatal(err)
	}
	applier := NewSkillApplier(&Discoverer{Sources: []Source{{Dir: skillsDir, Priority: 100}}})
	reg := resourceRegistry(t, applier)
	for _, resource := range []string{"", "../outside.md", "references/../../outside.md", `..\outside.md`, outside, "/outside.md", `C:\outside.md`, `C:outside.md`, "image.png"} {
		t.Run(resource, func(t *testing.T) {
			args, _ := json.Marshal(map[string]string{"name": "alpha", "resource": resource})
			result := callResourceTool(t, reg, string(args))
			if result.Err == nil || strings.Contains(result.Text, "OUTSIDE MUST STAY HIDDEN") {
				t.Fatalf("accepted %q: %+v", resource, result)
			}
		})
	}
	t.Run("symlink escape", func(t *testing.T) {
		if err := os.Symlink(outside, filepath.Join(dir, "escape.md")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		result := callResourceTool(t, reg, `{"name":"alpha","resource":"escape.md"}`)
		if result.Err == nil || strings.Contains(result.Text, "OUTSIDE MUST STAY HIDDEN") {
			t.Fatalf("accepted symlink escape: %+v", result)
		}
	})
	if len(applier.Applied()) != 0 {
		t.Fatal("resource-only calls unexpectedly activated guidance")
	}
}

func TestSkillResourceBoundsAndContinuation(t *testing.T) {
	root := t.TempDir()
	mkSkillDir(t, root, "large", "---\ndescription: resources\n---\nselected guidance")
	dir := filepath.Join(root, "large")
	var numbered strings.Builder
	for i := 1; i <= 650; i++ {
		fmt.Fprintf(&numbered, "line-%03d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "lines.md"), []byte(numbered.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wide.md"), []byte(strings.Repeat(strings.Repeat("ż", 1500)+"\n", 80)), 0600); err != nil {
		t.Fatal(err)
	}
	reg := resourceRegistry(t, NewSkillApplier(&Discoverer{Sources: []Source{{Dir: root, Priority: 100}}}))
	defaultRead := callResourceTool(t, reg, `{"name":"large","resource":"lines.md"}`)
	if defaultRead.Err != nil || !strings.Contains(defaultRead.Text, "line-300") || strings.Contains(defaultRead.Text, "line-301") || !strings.Contains(defaultRead.Text, `"from":301`) {
		t.Fatalf("default window: %+v", defaultRead)
	}
	capped := callResourceTool(t, reg, `{"name":"large","resource":"lines.md","to":650}`)
	if capped.Err != nil || !strings.Contains(capped.Text, "line-500") || strings.Contains(capped.Text, "line-501") || !strings.Contains(capped.Text, "range capped at 500") {
		t.Fatalf("range cap: %+v", capped)
	}
	tail := callResourceTool(t, reg, `{"name":"large","resource":"lines.md","from":501}`)
	if tail.Err != nil || !strings.Contains(tail.Text, "line-650") || !strings.Contains(tail.Text, "end of resource at line 650") {
		t.Fatalf("continuation: %+v", tail)
	}
	wide := callResourceTool(t, reg, `{"name":"large","resource":"wide.md"}`)
	if wide.Err != nil || len(wide.Text) > maxSkillResourceOutputBytes || !strings.Contains(wide.Text, "bytes on this line truncated") || !strings.Contains(wide.Text, "output capped") || !utf8.ValidString(wide.Text) || strings.Contains(wide.Text, "ctx_execute") {
		t.Fatalf("wide output: bytes=%d err=%v", len(wide.Text), wide.Err)
	}
	for _, args := range []string{`{"resource":"lines.md"}`, `{"name":"large","from":1}`, `{"name":"large","resource":"lines.md","from":3,"to":2}`} {
		if result := callResourceTool(t, reg, args); result.Err == nil {
			t.Fatalf("accepted invalid resource request: %s", args)
		}
	}
}
