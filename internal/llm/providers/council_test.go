package providers

import "testing"

func TestSaveLoadCouncilModels(t *testing.T) {
	home := t.TempDir()
	m := NewManager(home)
	if got := m.LoadCouncilModels(); len(got) != 0 {
		t.Fatalf("fresh manager roster = %v, want empty", got)
	}
	roster := []string{"lmstudio/qwen3", "openai/gpt-4o-mini"}
	if err := m.SaveCouncilModels(roster); err != nil {
		t.Fatalf("SaveCouncilModels: %v", err)
	}
	got := m.LoadCouncilModels()
	if len(got) != 2 || got[0] != "lmstudio/qwen3" || got[1] != "openai/gpt-4o-mini" {
		t.Errorf("LoadCouncilModels = %v", got)
	}
	// Overwrite (last selection wins).
	if err := m.SaveCouncilModels([]string{"x/y"}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got := m.LoadCouncilModels(); len(got) != 1 || got[0] != "x/y" {
		t.Errorf("after overwrite = %v", got)
	}
}
