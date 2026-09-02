package webgui

import (
	"context"
	"encoding/json"
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
	if _, ok := reg.Get("search_history"); !ok {
		t.Fatal("search_history must retrieve tool details omitted from future provider prompts")
	}
	if reg.IsVisible("search_history") {
		t.Fatal("search_history should stay discoverable and consume no ordinary-turn schema tokens")
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

	// NestCafe must expose the native Outlook COM tool through discovery.
	// Without this registration the model falls back to slow PowerShell COM
	// snippets, which can hit ctx_execute's hard timeout on large mailboxes.
	outlook, ok := reg.Get("outlook_mail")
	if !ok {
		t.Fatal("outlook_mail must be registered in the web/NestCafe loop")
	}
	if reg.IsVisible("outlook_mail") {
		t.Error("outlook_mail should stay discoverable, not consume every-turn schema budget")
	}
	if !strings.Contains(outlook.Schema, `"trash"`) || !strings.Contains(outlook.Schema, `"purge"`) ||
		!strings.Contains(outlook.Schema, `"count"`) || !strings.Contains(outlook.Schema, `"all"`) ||
		!strings.Contains(outlook.Schema, `"scope"`) || !strings.Contains(outlook.Schema, `"account"`) || !strings.Contains(outlook.Schema, `"all_stores"`) ||
		!strings.Contains(outlook.Schema, `"export_msg"`) || !strings.Contains(outlook.Schema, `"path"`) ||
		!strings.Contains(outlook.Schema, `"confirm"`) {
		t.Fatalf("outlook_mail schema lost safe bulk/account search support: %s", outlook.Schema)
	}
	searchTool, ok := reg.Get("tool_search")
	if !ok {
		t.Fatal("tool_search missing")
	}
	res, err := searchTool.Fn(context.Background(), json.RawMessage(`{"query":"outlook_mail"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("tool_search(outlook_mail) failed: result=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Text, `"name":"outlook_mail"`) || !reg.IsVisible("outlook_mail") {
		t.Fatalf("tool_search did not discover/activate outlook_mail: %s", res.Text)
	}

	thunderbird, ok := reg.Get("thunderbird_mail")
	if !ok {
		t.Fatal("thunderbird_mail must be registered in the web/NestCafe loop")
	}
	if reg.IsVisible("thunderbird_mail") {
		t.Error("thunderbird_mail should stay discoverable, not consume every-turn schema budget")
	}
	if !strings.Contains(thunderbird.Schema, `"status"`) || !strings.Contains(thunderbird.Schema, `"count"`) ||
		!strings.Contains(thunderbird.Schema, `"search"`) || !strings.Contains(thunderbird.Schema, `"attachments"`) || !strings.Contains(thunderbird.Schema, `"get_attachment"`) ||
		!strings.Contains(thunderbird.Schema, `"message_id"`) || !strings.Contains(thunderbird.Schema, `"part_name"`) || !strings.Contains(thunderbird.Schema, `"attachment_name"`) || !strings.Contains(thunderbird.Schema, `"senders"`) ||
		!strings.Contains(thunderbird.Schema, `"address"`) || !strings.Contains(thunderbird.Schema, `"to"`) || !strings.Contains(thunderbird.Schema, `"subject"`) ||
		!strings.Contains(thunderbird.Schema, `"create_folder"`) || !strings.Contains(thunderbird.Schema, `"rename_folder"`) || !strings.Contains(thunderbird.Schema, `"delete_folder"`) || !strings.Contains(thunderbird.Schema, `"new_name"`) ||
		!strings.Contains(thunderbird.Schema, `"move"`) || !strings.Contains(thunderbird.Schema, `"destination"`) || !strings.Contains(thunderbird.Schema, `"all"`) ||
		!strings.Contains(thunderbird.Schema, `"import_msg"`) || !strings.Contains(thunderbird.Schema, `"path"`) ||
		!strings.Contains(thunderbird.Schema, `"trash"`) || !strings.Contains(thunderbird.Schema, `"purge"`) ||
		!strings.Contains(thunderbird.Schema, `"empty_trash"`) || !strings.Contains(thunderbird.Schema, `"confirm"`) || !strings.Contains(thunderbird.Schema, `"batch_size"`) ||
		!strings.Contains(thunderbird.Schema, `"continuation"`) {
		t.Fatalf("thunderbird_mail schema lost safe Gmail operations: %s", thunderbird.Schema)
	}
	res, err = searchTool.Fn(context.Background(), json.RawMessage(`{"query":"thunderbird_mail"}`))
	if err != nil || res.Err != nil {
		t.Fatalf("tool_search(thunderbird_mail) failed: result=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Text, `"name":"thunderbird_mail"`) || !reg.IsVisible("thunderbird_mail") {
		t.Fatalf("tool_search did not discover/activate thunderbird_mail: %s", res.Text)
	}
}
