package memory

import (
	"path/filepath"
	"testing"
)

func TestWorkspace_UpsertAndFind(t *testing.T) {
	w := &Workspace{}
	w.Upsert(Project{Name: "alpha", Path: `C:\work\alpha`})
	w.Upsert(Project{Path: `C:\work\beta`}) // name derived from basename

	if len(w.Projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(w.Projects))
	}
	if w.Projects[1].Name != "beta" {
		t.Errorf("derived name = %q, want beta", w.Projects[1].Name)
	}
	// Resolve by path, name, basename.
	if _, ok := w.Get(`C:\work\alpha`); !ok {
		t.Error("Get by path failed")
	}
	if _, ok := w.Get("ALPHA"); !ok {
		t.Error("Get by name (case-insensitive) failed")
	}
	if _, ok := w.Get("beta"); !ok {
		t.Error("Get by basename failed")
	}
	if _, ok := w.Get("nope"); ok {
		t.Error("Get should miss for unknown target")
	}
}

func TestWorkspace_UpsertUpdatesNoWipe(t *testing.T) {
	w := &Workspace{}
	w.Upsert(Project{Name: "alpha", Path: `C:\work\alpha`, Model: "claude-opus-4-8", Provider: "anthropic"})
	// A bare re-add (no model/provider) must not wipe the stored preference.
	w.Upsert(Project{Name: "alpha", Path: `C:\work\alpha`})
	p, _ := w.Get("alpha")
	if p.Model != "claude-opus-4-8" || p.Provider != "anthropic" {
		t.Fatalf("bare re-add wiped preferences: %+v", p)
	}
	if len(w.Projects) != 1 {
		t.Fatalf("re-add duplicated project: %d", len(w.Projects))
	}
}

func TestWorkspace_SetActiveAndRemove(t *testing.T) {
	w := &Workspace{}
	w.Upsert(Project{Name: "alpha", Path: `C:\work\alpha`})
	w.Upsert(Project{Name: "beta", Path: `C:\work\beta`})

	if _, ok := w.SetActive("beta"); !ok {
		t.Fatal("SetActive(beta) failed")
	}
	if ap, ok := w.ActiveProject(); !ok || ap.Name != "beta" {
		t.Fatalf("ActiveProject = %+v ok=%v, want beta", ap, ok)
	}
	// Removing the active project clears Active.
	if _, ok := w.Remove("beta"); !ok {
		t.Fatal("Remove(beta) failed")
	}
	if w.Active != "" {
		t.Errorf("Active should be cleared after removing active project, got %q", w.Active)
	}
	if _, ok := w.ActiveProject(); ok {
		t.Error("ActiveProject should be empty after removal")
	}
}

func TestWorkspace_LoadSaveRoundTrip(t *testing.T) {
	home := t.TempDir()
	w := &Workspace{}
	w.Upsert(Project{Name: "alpha", Path: filepath.Join(home, "alpha"), Model: "claude-sonnet-4-6"})
	w.SetActive("alpha")
	if err := SaveWorkspace(home, w); err != nil {
		t.Fatal(err)
	}
	got := LoadWorkspace(home)
	if len(got.Projects) != 1 || got.Projects[0].Model != "claude-sonnet-4-6" {
		t.Fatalf("round-trip lost data: %+v", got)
	}
	ap, ok := got.ActiveProject()
	if !ok || ap.Name != "alpha" {
		t.Fatalf("active not restored: %+v ok=%v", ap, ok)
	}
}

func TestWorkspace_LoadMissingIsEmpty(t *testing.T) {
	w := LoadWorkspace(t.TempDir())
	if w == nil || len(w.Projects) != 0 || w.Active != "" {
		t.Fatalf("missing workspace should be empty, got %+v", w)
	}
}

func TestWorkspace_LoadDropsDanglingActive(t *testing.T) {
	home := t.TempDir()
	// Write a workspace whose Active points at a non-existent project.
	if err := SaveWorkspace(home, &Workspace{
		Projects: []Project{{Name: "alpha", Path: `C:\work\alpha`}},
		Active:   `C:\work\ghost`,
	}); err != nil {
		t.Fatal(err)
	}
	w := LoadWorkspace(home)
	if w.Active != "" {
		t.Errorf("dangling Active should be dropped, got %q", w.Active)
	}
}

func TestWorkspaceRelocatePreservesMetadataAndActiveProject(t *testing.T) {
	w := &Workspace{Projects: []Project{{Name: "USB", Path: `E:\work`, Model: "m", Provider: "p"}}, Active: `E:\work`}
	old, moved, ok := w.Relocate(`E:\work`, `F:\work`)
	if !ok {
		t.Fatal("Relocate returned false")
	}
	if old.Path != `E:\work` || moved.Path != `F:\work` || moved.Name != "USB" || moved.Model != "m" || moved.Provider != "p" {
		t.Fatalf("relocated project = old:%+v new:%+v", old, moved)
	}
	if w.Active != `F:\work` {
		t.Fatalf("active = %q", w.Active)
	}
}
