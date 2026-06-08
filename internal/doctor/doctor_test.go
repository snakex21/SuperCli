package doctor

import (
	"context"
	"strings"
	"testing"

	"supercli/internal/tools"
)

func TestRunIncludesCoreChecks(t *testing.T) {
	dir := t.TempDir()
	rep := Run(context.Background(), Env{Version: "test", Home: dir, DataDir: dir, Registry: tools.NewRegistry()})
	if rep.Version != "test" {
		t.Fatalf("version = %q", rep.Version)
	}
	names := map[string]bool{}
	for _, c := range rep.Checks {
		names[c.Name] = true
	}
	for _, name := range []string{"binary", "runtime", "home", "data dir", "tools"} {
		if !names[name] {
			t.Fatalf("missing check %q in %+v", name, rep.Checks)
		}
	}
}

func TestRenderPlain(t *testing.T) {
	rep := Report{Version: "x", Checks: []Check{{Name: "home", Status: OK, Detail: "/tmp"}, {Name: "provider", Status: Warn, Detail: "echo"}}}
	out := RenderPlain(rep)
	if !strings.Contains(out, "supercli x") || !strings.Contains(out, "home") || !strings.Contains(out, "provider") {
		t.Fatalf("unexpected render: %q", out)
	}
}
