package webgui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProjectMCPResolutionMergesGlobalAndOverridesWholeServer(t *testing.T) {
	data, a, b := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(data, "config.toml"), `[mcp.servers.shared]
command = "global-command"
args = ["global"]
allowed_tools = ["global-tool"]
[mcp.servers.shared.env]
TOKEN = "global"
[mcp.servers.global-only]
command = "global-only-command"
`)
	write(filepath.Join(a, ".supercli", "config.toml"), `[mcp.servers.shared]
command = "project-a-command"
args = ["a"]
allowed_tools = ["a-tool"]
[mcp.servers.shared.env]
TOKEN = "a"
[mcp.servers.a-only]
command = "a-only-command"
`)
	eng, err := NewEngine(echoConfig(), a, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	ma, err := eng.loadMCPRuntime(a)
	if err != nil {
		t.Fatal(err)
	}
	defer ma.Close()
	mb, err := eng.loadMCPRuntime(b)
	if err != nil {
		t.Fatal(err)
	}
	defer mb.Close()
	sa, _ := ma.Get("shared")
	sb, _ := mb.Get("shared")
	if sa == sb || sa.Config.Command != "project-a-command" || sb.Config.Command != "global-command" {
		t.Fatalf("shared server resolution: A=%+v B=%+v", sa.Config, sb.Config)
	}
	if !reflect.DeepEqual(sa.Config.Args, []string{"a"}) || !reflect.DeepEqual(sa.Config.AllowedTools, []string{"a-tool"}) || sa.Config.Env["TOKEN"] != "a" {
		t.Fatalf("project override lost fields: %+v", sa.Config)
	}
	if sa.Config.Dir != a || sb.Config.Dir != b {
		t.Fatalf("servers not pinned to project directories: %q %q", sa.Config.Dir, sb.Config.Dir)
	}
	if _, ok := ma.Get("global-only"); !ok {
		t.Fatal("project config dropped global server")
	}
	if _, ok := ma.Get("a-only"); !ok {
		t.Fatal("project server missing")
	}
	if _, ok := mb.Get("a-only"); ok {
		t.Fatal("project A server leaked to B")
	}
	if sa.Status().Running || sb.Status().Running {
		t.Fatal("configuration resolution started a subprocess")
	}
}
