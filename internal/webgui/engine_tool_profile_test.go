package webgui

import (
	"strings"
	"testing"
)

// legacyEditors are the line-oriented editors the web loop used to
// advertise every turn, alongside patch_file. Seven interchangeable edit
// paths is what made a small model pick the wrong one; the TUI core
// (agent.thinCoreTools) has carried exactly one for a long time.
var legacyEditors = []string{"edit_line", "edit_lines", "insert_after", "delete_lines", "write_file"}

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
	for _, name := range legacyEditors {
		if strings.Contains(visible, "|"+name+"|") {
			t.Errorf("%s is still always-on in the web profile: %s", name, visible)
		}
	}

	// Not always-on is not the same as gone: every legacy editor stays
	// registered, so workers, checkpoint replay and tool_search all still
	// reach it.
	reg := eng.diagnosticRegistry
	if reg == nil {
		t.Fatal("diagnostic registry not captured")
	}
	for _, name := range legacyEditors {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("%s is no longer registered", name)
		}
	}

	// And tool_search can bring one back for the turn that needs it.
	reg.Activate("edit_line")
	if !reg.IsVisible("edit_line") {
		t.Error("edit_line cannot be activated through tool_search")
	}
}
