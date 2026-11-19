package config

import (
	"reflect"
	"testing"
)

func TestValidateProviderName(t *testing.T) {
	for _, ok := range []string{"work", "My Provider", "a.b_c-d", "Zażółć 2", "x"} {
		if err := ValidateProviderName(ok); err != nil {
			t.Errorf("ValidateProviderName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "   ", " lead", "trail ", "a/b", "a:b", "a\tb", "semi;colon", "quote\"", string(make([]rune, 65))} {
		if err := ValidateProviderName(bad); err == nil {
			t.Errorf("ValidateProviderName(%q) = nil, want error", bad)
		}
	}
	long := ""
	for i := 0; i < MaxProviderNameLength+1; i++ {
		long += "a"
	}
	if ValidateProviderName(long) == nil {
		t.Error("over-long name accepted")
	}
}

func TestRenameProviderRefsRewritesEveryReference(t *testing.T) {
	tc := TomlConfig{
		DefaultProvider:   "old",
		DefaultModel:      "m1",
		Providers:         []ProviderConf{{Name: "old", Model: "m1"}, {Name: "other"}},
		TaskModel:         "old/worker",
		AdvisorModel:      "old/adv",
		ReviewModel:       "other/rev",
		OrchestratorModel: "old/orch",
		CompactModel:      "bare-model",
		DarwinJudgeModel:  "old/judge",
		FallbackModels:    []string{"old/a", "other/b", "oldish/c", "old"},
		DarwinModels:      []string{"old/x/y"},
		Council:           CouncilConf{Models: []string{"old/c1", "c2"}},
		ModelPrices:       []ModelPriceConf{{Provider: "old", Model: "m1"}, {Provider: "", Model: "m2"}},
		MediaGeneration:   MediaGenerationConf{Image: &MediaGenerationProviderConf{APIKeyProvider: "old"}},
	}
	if !RenameProviderRefs(&tc, "old", "new") {
		t.Fatal("RenameProviderRefs reported no change")
	}
	if tc.DefaultProvider != "new" || tc.Providers[0].Name != "new" || tc.Providers[1].Name != "other" {
		t.Fatalf("provider/default not renamed: %+v %q", tc.Providers, tc.DefaultProvider)
	}
	if tc.DefaultModel != "m1" {
		t.Fatalf("bare default_model changed: %q", tc.DefaultModel)
	}
	for got, want := range map[string]string{
		tc.TaskModel: "new/worker", tc.AdvisorModel: "new/adv", tc.ReviewModel: "other/rev",
		tc.OrchestratorModel: "new/orch", tc.CompactModel: "bare-model", tc.DarwinJudgeModel: "new/judge",
	} {
		if got != want {
			t.Errorf("model ref = %q, want %q", got, want)
		}
	}
	if !reflect.DeepEqual(tc.FallbackModels, []string{"new/a", "other/b", "oldish/c", "old"}) {
		t.Errorf("fallback_models = %v", tc.FallbackModels)
	}
	if !reflect.DeepEqual(tc.DarwinModels, []string{"new/x/y"}) || !reflect.DeepEqual(tc.Council.Models, []string{"new/c1", "c2"}) {
		t.Errorf("darwin/council = %v %v", tc.DarwinModels, tc.Council.Models)
	}
	if tc.ModelPrices[0].Provider != "new" || tc.ModelPrices[1].Provider != "" {
		t.Errorf("model prices = %+v", tc.ModelPrices)
	}
	if tc.MediaGeneration.Image.APIKeyProvider != "new" {
		t.Errorf("media api_key_provider = %q", tc.MediaGeneration.Image.APIKeyProvider)
	}
	untouched := TomlConfig{DefaultProvider: "other", TaskModel: "Old/x"}
	if RenameProviderRefs(&untouched, "old", "new") {
		t.Fatal("config without references (case differs) must be unchanged")
	}
}

func TestModelContextStoreRenameProvider(t *testing.T) {
	dir := t.TempDir()
	s := LoadModelContextStore(dir)
	if err := s.Set("Old", "m", 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("keep", "m", 2048); err != nil {
		t.Fatal(err)
	}
	n, err := s.RenameProvider("old", "new")
	if err != nil || n != 1 {
		t.Fatalf("RenameProvider = %d, %v", n, err)
	}
	reloaded := LoadModelContextStore(dir)
	if tokens, ok := reloaded.Get("new", "m"); !ok || tokens != 4096 {
		t.Fatalf("budget not moved: %d %v", tokens, ok)
	}
	if _, ok := reloaded.Get("old", "m"); ok {
		t.Fatal("old budget still present")
	}
	if tokens, _ := reloaded.Get("keep", "m"); tokens != 2048 {
		t.Fatal("unrelated budget changed")
	}
}
