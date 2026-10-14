package config

import "testing"

func TestReviewDisabled(t *testing.T) {
	for _, v := range []string{"", "  ", "off", " OFF ", "Off"} {
		if !ReviewDisabled(TomlConfig{ReviewModel: v}) {
			t.Errorf("review_model %q must be off", v)
		}
	}
	for _, v := range []string{"gpt-5", "zen/claude", "offline-model"} {
		if ReviewDisabled(TomlConfig{ReviewModel: v}) {
			t.Errorf("review_model %q must be on", v)
		}
	}
}

func TestModeHelpers(t *testing.T) {
	var c TomlConfig
	if AdvisorDisabled(c) || DarwinEnabled(c) {
		t.Fatal("defaults: advisor available, darwin off")
	}
	on := true
	c.Darwin = &on
	if !DarwinEnabled(c) {
		t.Fatal("explicit darwin = true must enable it")
	}
	c.AdvisorModel = " OFF "
	off := false
	c.Darwin = &off
	if !AdvisorDisabled(c) || DarwinEnabled(c) {
		t.Fatal("explicit off must disable both")
	}
	c.AdvisorModel = "lmstudio/qwen"
	if AdvisorDisabled(c) {
		t.Fatal("a model reference keeps the advisor on")
	}
	dst := TomlConfig{}
	mergeToml(&dst, TomlConfig{Darwin: &off})
	if DarwinEnabled(dst) {
		t.Fatal("merge must carry darwin = false")
	}
}
