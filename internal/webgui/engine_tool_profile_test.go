package webgui

import (
	"strings"
	"testing"
)

// retiredEditors are the line-oriented editors the web loop used to advertise
// every turn alongside patch_file. Seven interchangeable edit paths is what
// made a small model pick the wrong one; the TUI core (agent.thinCoreTools)
// has carried exactly one for a long time, and now the others do not exist at
// all. Nothing can register them, so nothing can put them back in front of the
// model — not tool_search, not a worker registry.
var retiredEditors = []string{"edit_line", "edit_lines", "insert_after", "delete_lines"}

func TestEngine_WebEditorProfileMatchesTUI(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	loop, err := eng.newLoop()
	if err != nil {
		t.Fatal(err)
	}

	visible := "|" + strings.Join(loop.VisibleToolNames(), "|") + "|"
	for _, name := range []string{"patch_file", "create_file"} {
		if !strings.Contains(visible, "|"+name+"|") {
			t.Errorf("web profile lost its edit path %s: %s", name, visible)
		}
	}
	for _, name := range append(append([]string{}, retiredEditors...), "write_file") {
		if strings.Contains(visible, "|"+name+"|") {
			t.Errorf("%s is always-on in the web profile: %s", name, visible)
		}
	}

	reg := eng.diagnosticRegistry
	if reg == nil {
		t.Fatal("diagnostic registry not captured")
	}
	for _, name := range retiredEditors {
		if _, ok := reg.Get(name); ok {
			t.Errorf("%s is registered again — the consolidation is one edit path, not a hidden one", name)
		}
	}

	// write_file is the exception that proves the rule: a whole-file rewrite
	// is a real capability, so it stays registered and reachable through
	// tool_search, just off the per-turn schema budget.
	if _, ok := reg.Get("write_file"); !ok {
		t.Fatal("write_file must stay registered for whole-file rewrites")
	}
	reg.Activate("write_file")
	if !reg.IsVisible("write_file") {
		t.Error("write_file cannot be activated through tool_search")
	}
}
