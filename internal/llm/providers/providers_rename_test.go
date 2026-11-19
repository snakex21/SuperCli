package providers

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

// renameTestSecret is a fake credential. Tests compare it by equality only
// and never print it.
const renameTestSecret = "sk-rename-test-0123456789"

func newRenameManager(t *testing.T) (*Manager, string, string) {
	t.Helper()
	dataDir := t.TempDir()
	m := NewManager(dataDir)
	global := m.tomlPath
	if err := config.SaveToml(global, config.TomlConfig{
		DefaultProvider: "work",
		DefaultModel:    "m1",
		TaskModel:       "work/worker-model",
		FallbackModels:  []string{"work/fb", "home/fb"},
		Council:         config.CouncilConf{Models: []string{"work/c1"}},
		ModelPrices:     []config.ModelPriceConf{{Provider: "work", Model: "m1", InputCost: 1}},
		Providers: []config.ProviderConf{
			{Name: "work", Type: "openai", BaseURL: "http://127.0.0.1:1/v1", APIKey: renameTestSecret, Model: "m1"},
			{Name: "home", Type: "openai", BaseURL: "http://127.0.0.1:2/v1"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(t.TempDir(), ".supercli", "config.toml")
	if err := config.SaveToml(project, config.TomlConfig{DefaultProvider: "work", DefaultModel: "m1", ReviewModel: "work/rev"}); err != nil {
		t.Fatal(err)
	}
	m.SetActiveConfigPath(project)
	m.Reload()
	m.HideModelFor("work", "hidden-model")
	m.HideModelFor("home", "hidden-model")
	return m, global, project
}

func TestManagerRenameUpdatesConfigLayersAndMovesKey(t *testing.T) {
	m, global, project := newRenameManager(t)
	if err := m.Rename("work", "Work Cloud"); err != nil {
		t.Fatal(err)
	}
	g, err := config.LoadToml(global)
	if err != nil {
		t.Fatal(err)
	}
	if g.DefaultProvider != "Work Cloud" || g.TaskModel != "Work Cloud/worker-model" ||
		g.FallbackModels[0] != "Work Cloud/fb" || g.FallbackModels[1] != "home/fb" ||
		g.Council.Models[0] != "Work Cloud/c1" || g.ModelPrices[0].Provider != "Work Cloud" {
		t.Fatalf("global references not rewritten: default=%q task=%q fallback=%v council=%v", g.DefaultProvider, g.TaskModel, g.FallbackModels, g.Council.Models)
	}
	var renamedKeyOK, oldPresent bool
	for _, p := range g.Providers {
		if p.Name == "Work Cloud" {
			renamedKeyOK = p.APIKey == renameTestSecret && p.Model == "m1"
		}
		if p.Name == "work" {
			oldPresent = true
		}
	}
	if !renamedKeyOK || oldPresent {
		t.Fatalf("API key did not move to the new name (moved=%v, old entry left=%v)", renamedKeyOK, oldPresent)
	}
	if key, ok := m.APIKey("Work Cloud"); !ok || key != renameTestSecret {
		t.Fatal("in-memory key not available under the new name")
	}
	if _, ok := m.APIKey("work"); ok {
		t.Fatal("old name still resolves a key")
	}
	p, err := config.LoadToml(project)
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultProvider != "Work Cloud" || p.ReviewModel != "Work Cloud/rev" {
		t.Fatalf("project references not rewritten: %q %q", p.DefaultProvider, p.ReviewModel)
	}
	if !m.IsHiddenFor("Work Cloud", "hidden-model") || m.IsHiddenFor("work", "hidden-model") || !m.IsHiddenFor("home", "hidden-model") {
		t.Fatal("hidden model state not renamed")
	}
	fresh := NewManager(filepath.Dir(global))
	fresh.Reload()
	fresh.LoadHiddenState()
	if !fresh.IsHiddenFor("Work Cloud", "hidden-model") || fresh.IsHiddenFor("work", "hidden-model") {
		t.Fatal("persisted hidden state not renamed")
	}
}

func TestManagerRenameValidation(t *testing.T) {
	m, global, _ := newRenameManager(t)
	before, _ := os.ReadFile(global)
	cases := map[string]string{
		"HOME":     "already exists",
		"a/b":      "letters, digits",
		"bad:name": "letters, digits",
		"":         "required",
	}
	for newName, want := range cases {
		err := m.Rename("work", newName)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Rename(work, %q) = %v, want error containing %q", newName, err, want)
		}
	}
	if err := m.Rename("missing", "x"); err == nil {
		t.Error("renaming a missing provider succeeded")
	}
	if err := m.Rename("work", "work"); err != nil {
		t.Errorf("same-name rename = %v, want no-op", err)
	}
	after, _ := os.ReadFile(global)
	if string(before) != string(after) {
		t.Fatal("rejected or no-op renames changed the config file")
	}
	// Changing only the letter case of the same provider is allowed.
	if err := m.Rename("work", "WORK"); err != nil {
		t.Fatalf("case-only rename: %v", err)
	}
}

type fakeSessionRenamer struct {
	err      error
	old, new string
}

func (f *fakeSessionRenamer) RenameProvider(oldName, newName string) (int64, error) {
	f.old, f.new = oldName, newName
	return 1, f.err
}

type fakeBudgetRenamer struct{ old, new string }

func (f *fakeBudgetRenamer) RenameProvider(oldName, newName string) (int, error) {
	f.old, f.new = oldName, newName
	return 1, nil
}

func TestRenameProviderEverywhereUpdatesStoresAndRegistry(t *testing.T) {
	m, _, _ := newRenameManager(t)
	caps := llm.NewCapabilityRegistry()
	caps.Register(llm.ModelInfo{ID: "m1", Provider: "work"})
	sessions, budgets := &fakeSessionRenamer{}, &fakeBudgetRenamer{}
	if err := RenameProviderEverywhere(m, "work", "renamed", sessions, budgets, caps); err != nil {
		t.Fatal(err)
	}
	if sessions.old != "work" || sessions.new != "renamed" || budgets.old != "work" || budgets.new != "renamed" {
		t.Fatalf("stores not updated: %+v %+v", sessions, budgets)
	}
	if mi, ok := caps.Get("m1"); !ok || mi.Provider != "renamed" {
		t.Fatalf("capability entry not relabelled: %+v", mi)
	}
}

func TestRenameProviderEverywhereRollsBackWhenSessionsFail(t *testing.T) {
	m, global, _ := newRenameManager(t)
	sessions := &fakeSessionRenamer{err: errors.New("database is locked")}
	if err := RenameProviderEverywhere(m, "work", "renamed", sessions, nil, nil); err == nil {
		t.Fatal("expected error")
	}
	g, _ := config.LoadToml(global)
	if g.DefaultProvider != "work" || g.TaskModel != "work/worker-model" {
		t.Fatalf("config rename was not reverted: %q %q", g.DefaultProvider, g.TaskModel)
	}
	if key, ok := m.APIKey("work"); !ok || key != renameTestSecret {
		t.Fatal("key not back under the original name after rollback")
	}
}
