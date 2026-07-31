package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSampling_RoundTrip: unset parameters must survive a save/load
// cycle as unset, so persisting any unrelated setting can never
// materialize a sampling value the user never chose.
func TestSampling_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := SaveToml(p, TomlConfig{DefaultModel: "m"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	for _, key := range []string{"temperature", "top_p", "top_k", "min_p", "seed", "repeat_penalty"} {
		if strings.Contains(string(b), key) {
			t.Errorf("empty config persisted %q:\n%s", key, b)
		}
	}
	c, err := LoadToml(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !c.Sampling.IsZero() {
		t.Errorf("empty sampling came back set: %+v", c.Sampling)
	}

	temp, topK := 0.6, 40
	if err := SaveToml(p, TomlConfig{DefaultModel: "m", Sampling: SamplingConf{Temperature: &temp, TopK: &topK}}); err != nil {
		t.Fatal(err)
	}
	c, err = LoadToml(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c.Sampling.Temperature == nil || *c.Sampling.Temperature != 0.6 || c.Sampling.TopK == nil || *c.Sampling.TopK != 40 {
		t.Errorf("round trip lost values: %+v", c.Sampling)
	}
	if c.Sampling.TopP != nil {
		t.Errorf("top_p materialized out of nothing: %v", *c.Sampling.TopP)
	}
}

// TestSampling_MergeIsPerField: a project config may raise temperature
// without discarding the global config's top_p.
func TestSampling_MergeIsPerField(t *testing.T) {
	global, project := 0.2, 0.9
	topP := 0.95
	dst := TomlConfig{Sampling: SamplingConf{Temperature: &global, TopP: &topP}}
	mergeToml(&dst, TomlConfig{Sampling: SamplingConf{Temperature: &project}})
	if dst.Sampling.Temperature == nil || *dst.Sampling.Temperature != 0.9 {
		t.Errorf("temperature not overridden: %+v", dst.Sampling)
	}
	if dst.Sampling.TopP == nil || *dst.Sampling.TopP != 0.95 {
		t.Errorf("top_p clobbered by the merge: %+v", dst.Sampling)
	}
}

// TestSampling_EnvTemperatureWins pins the documented hierarchy:
// env/flag override beats the config file.
func TestSampling_EnvTemperatureWins(t *testing.T) {
	file, env := 0.2, 0.8
	got := SamplingConf{Temperature: &file, TopP: &env}.Resolve(&env)
	if got.Temperature == nil || *got.Temperature != 0.8 {
		t.Errorf("env temperature lost: %s", got)
	}
	got = SamplingConf{Temperature: &file}.Resolve(nil)
	if got.Temperature == nil || *got.Temperature != 0.2 {
		t.Errorf("file temperature lost: %s", got)
	}
	if !(SamplingConf{}).Resolve(nil).IsZero() {
		t.Error("empty config resolved to a non-empty sampling")
	}
}
