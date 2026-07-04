package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadToml_Missing(t *testing.T) {
	cfg, err := LoadToml("/nonexistent/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "" {
		t.Errorf("expected empty, got %q", cfg.DefaultModel)
	}
}

func TestLoadToml_Empty(t *testing.T) {
	cfg, err := LoadToml("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "" {
		t.Errorf("expected empty, got %q", cfg.DefaultModel)
	}
}

func TestLoadToml_Full(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
default_model = "gpt-4o"
draft_mode = "critical"
draft_model = "gpt-4o-mini"
max_credits_per_session = 50000
max_credits_per_day = 200000
no_color = true
provider = "openai"
debug = true
max_steps = 15

[[providers]]
name = "main"
type = "openai"
base_url = "https://api.openai.com/v1"
api_key = "sk-test"
model = "gpt-4o"

[[model_prices]]
model = "gpt-4o"
input_cost = 5.0
output_cost = 15.0

[[model_prices]]
model = "gpt-4o-mini"
input_cost = 0.5
output_cost = 1.5
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultModel != "gpt-4o" {
		t.Errorf("DefaultModel = %q", cfg.DefaultModel)
	}
	if cfg.DraftMode != "critical" {
		t.Errorf("DraftMode = %q", cfg.DraftMode)
	}
	if cfg.DraftModel != "gpt-4o-mini" {
		t.Errorf("DraftModel = %q", cfg.DraftModel)
	}
	if cfg.MaxCreditsPerSession != 50000 {
		t.Errorf("MaxCreditsPerSession = %d", cfg.MaxCreditsPerSession)
	}
	if cfg.MaxCreditsPerDay != 200000 {
		t.Errorf("MaxCreditsPerDay = %d", cfg.MaxCreditsPerDay)
	}
	if !cfg.NoColor {
		t.Error("NoColor should be true")
	}
	if cfg.Provider != "openai" {
		t.Errorf("Provider = %q", cfg.Provider)
	}
	if !cfg.Debug {
		t.Error("Debug should be true")
	}
	if cfg.MaxSteps != 15 {
		t.Errorf("MaxSteps = %d", cfg.MaxSteps)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("Providers len = %d, want 1", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "main" {
		t.Errorf("Provider[0].Name = %q", cfg.Providers[0].Name)
	}
	if cfg.Providers[0].APIKey != "sk-test" {
		t.Errorf("Provider[0].APIKey = %q", cfg.Providers[0].APIKey)
	}
	if len(cfg.ModelPrices) != 2 {
		t.Fatalf("ModelPrices len = %d, want 2", len(cfg.ModelPrices))
	}
	if cfg.ModelPrices[0].InputCost != 5.0 {
		t.Errorf("ModelPrices[0].InputCost = %v", cfg.ModelPrices[0].InputCost)
	}
}

func TestLoadToml_Invalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	os.WriteFile(path, []byte("{{{{not toml"), 0o644)
	_, err := LoadToml(path)
	if err == nil {
		t.Error("expected error for invalid TOML")
	}
}

func TestSaveToml_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	original := TomlConfig{
		DefaultModel:         "claude-sonnet-4-5",
		DraftMode:            "balanced",
		MaxCreditsPerSession: 100000,
		NoColor:              true,
		Providers: []ProviderConf{
			{Name: "test", Type: "openai", BaseURL: "http://x", APIKey: "k"},
		},
		ModelPrices: []ModelPriceConf{
			{Model: "m1", InputCost: 1.0, OutputCost: 2.0},
		},
	}
	if err := SaveToml(path, original); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DefaultModel != original.DefaultModel {
		t.Errorf("DefaultModel = %q", loaded.DefaultModel)
	}
	if loaded.MaxCreditsPerSession != original.MaxCreditsPerSession {
		t.Errorf("MaxCreditsPerSession = %d", loaded.MaxCreditsPerSession)
	}
	if !loaded.NoColor {
		t.Error("NoColor should be true")
	}
	if len(loaded.Providers) != 1 {
		t.Fatalf("Providers len = %d", len(loaded.Providers))
	}
	if loaded.Providers[0].APIKey != "k" {
		t.Errorf("Provider APIKey = %q", loaded.Providers[0].APIKey)
	}
	if len(loaded.ModelPrices) != 1 {
		t.Fatalf("ModelPrices len = %d", len(loaded.ModelPrices))
	}
}

// TestSaveToml_OrchestratorTriState: the orchestrator switch persists
// as a tri-state *bool (nil = default OFF, explicit true/false).
func TestSaveToml_OrchestratorTriState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Absent → nil after round trip.
	if err := SaveToml(path, TomlConfig{DefaultModel: "m"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Orchestrator != nil {
		t.Errorf("absent orchestrator should load as nil, got %v", *loaded.Orchestrator)
	}

	// Explicit true survives the round trip.
	on := true
	if err := SaveToml(path, TomlConfig{DefaultModel: "m", Orchestrator: &on}); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Orchestrator == nil || !*loaded.Orchestrator {
		t.Errorf("orchestrator=true did not round-trip: %v", loaded.Orchestrator)
	}
}

// TestMergeToml_OrchestratorOverride: a non-nil source orchestrator
// overrides the destination; nil leaves it untouched.
func TestMergeToml_OrchestratorOverride(t *testing.T) {
	on := true
	off := false
	dst := TomlConfig{Orchestrator: &on}
	mergeToml(&dst, TomlConfig{}) // nil src leaves dst
	if dst.Orchestrator == nil || !*dst.Orchestrator {
		t.Fatalf("nil src should not clear orchestrator: %v", dst.Orchestrator)
	}
	mergeToml(&dst, TomlConfig{Orchestrator: &off})
	if dst.Orchestrator == nil || *dst.Orchestrator {
		t.Fatalf("src=false should override to false: %v", dst.Orchestrator)
	}
}

// TestMergeToml_DarwinParallelOverride: a non-nil source darwin_parallel
// overrides the destination in both directions; nil leaves it untouched.
func TestMergeToml_DarwinParallelOverride(t *testing.T) {
	on := true
	off := false
	dst := TomlConfig{DarwinParallel: &off}
	mergeToml(&dst, TomlConfig{}) // nil src leaves dst
	if dst.DarwinParallel == nil || *dst.DarwinParallel {
		t.Fatalf("nil src should not clear darwin_parallel: %v", dst.DarwinParallel)
	}
	mergeToml(&dst, TomlConfig{DarwinParallel: &on})
	if dst.DarwinParallel == nil || !*dst.DarwinParallel {
		t.Fatalf("src=true should override to true: %v", dst.DarwinParallel)
	}
}

// TestMergeToml_TaskParallelOverride: a non-nil source task_parallel
// overrides the destination in both directions; nil leaves it untouched.
func TestMergeToml_TaskParallelOverride(t *testing.T) {
	on := true
	off := false
	dst := TomlConfig{TaskParallel: &off}
	mergeToml(&dst, TomlConfig{}) // nil src leaves dst
	if dst.TaskParallel == nil || *dst.TaskParallel {
		t.Fatalf("nil src should not clear task_parallel: %v", dst.TaskParallel)
	}
	mergeToml(&dst, TomlConfig{TaskParallel: &on})
	if dst.TaskParallel == nil || !*dst.TaskParallel {
		t.Fatalf("src=true should override to true: %v", dst.TaskParallel)
	}
}

// TestMergeToml_CachePromptOverride: a non-nil source cache_prompt
// overrides the destination in both directions; nil leaves it untouched.
func TestMergeToml_CachePromptOverride(t *testing.T) {
	on := true
	off := false
	dst := TomlConfig{CachePrompt: &off}
	mergeToml(&dst, TomlConfig{}) // nil src leaves dst
	if dst.CachePrompt == nil || *dst.CachePrompt {
		t.Fatalf("nil src should not clear cache_prompt: %v", dst.CachePrompt)
	}
	mergeToml(&dst, TomlConfig{CachePrompt: &on})
	if dst.CachePrompt == nil || !*dst.CachePrompt {
		t.Fatalf("src=true should override to true: %v", dst.CachePrompt)
	}
}

func TestFindTomlPaths(t *testing.T) {
	global, project := FindTomlPaths("/data/supercli-data", "/home/user/project")
	wantGlobal := filepath.Join("/data/supercli-data", "config.toml")
	wantProject := filepath.Join("/home/user", "project", ".supercli", "config.toml")
	if global != wantGlobal {
		t.Errorf("global = %q, want %q", global, wantGlobal)
	}
	if project != wantProject {
		t.Errorf("project = %q, want %q", project, wantProject)
	}
}

func TestFindTomlPaths_SameDir(t *testing.T) {
	// When the project config would resolve to the same file as the
	// global one (data dir = <cwd>/.supercli), project must be empty.
	global, project := FindTomlPaths("/home/user/.supercli", "/home/user")
	if project != "" {
		t.Errorf("project should be empty when paths coincide, got %q", project)
	}
	_ = global
}

func TestMergeToml_ProjectOverridesGlobal(t *testing.T) {
	dst := TomlConfig{
		DefaultModel: "gpt-4o-mini",
		DraftMode:    "off",
		Provider:     "echo",
	}
	src := TomlConfig{
		DefaultModel: "gpt-4o",
		DraftMode:    "critical",
		Provider:     "openai",
	}
	mergeToml(&dst, src)
	if dst.DefaultModel != "gpt-4o" {
		t.Errorf("DefaultModel = %q, want gpt-4o", dst.DefaultModel)
	}
	if dst.DraftMode != "critical" {
		t.Errorf("DraftMode = %q, want critical", dst.DraftMode)
	}
	if dst.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", dst.Provider)
	}
}

func TestMergeToml_SkipsZeroValues(t *testing.T) {
	dst := TomlConfig{
		DefaultModel: "gpt-4o",
		DraftMode:    "balanced",
	}
	src := TomlConfig{} // all zeros
	mergeToml(&dst, src)
	if dst.DefaultModel != "gpt-4o" {
		t.Errorf("DefaultModel should be unchanged, got %q", dst.DefaultModel)
	}
	if dst.DraftMode != "balanced" {
		t.Errorf("DraftMode should be unchanged, got %q", dst.DraftMode)
	}
}

func TestMergeToml_MemoryBriefingTokensOverride(t *testing.T) {
	// A positive project value overrides the global one; zero is
	// skipped so the tier default stands.
	dst := TomlConfig{MemoryBriefingTokens: 700}
	mergeToml(&dst, TomlConfig{MemoryBriefingTokens: 200})
	if dst.MemoryBriefingTokens != 200 {
		t.Errorf("MemoryBriefingTokens override = %d, want 200", dst.MemoryBriefingTokens)
	}
	mergeToml(&dst, TomlConfig{}) // zero must not clobber
	if dst.MemoryBriefingTokens != 200 {
		t.Errorf("zero clobbered MemoryBriefingTokens: %d", dst.MemoryBriefingTokens)
	}
}

func TestMergeToml_ProvidersOverrideEntirely(t *testing.T) {
	dst := TomlConfig{
		Providers: []ProviderConf{
			{Name: "old"},
		},
	}
	src := TomlConfig{
		Providers: []ProviderConf{
			{Name: "new"},
		},
	}
	mergeToml(&dst, src)
	if len(dst.Providers) != 1 || dst.Providers[0].Name != "new" {
		t.Errorf("providers not overridden: %+v", dst.Providers)
	}
}

func TestMergeToml_NoColorProjectSetsTrue(t *testing.T) {
	dst := TomlConfig{NoColor: false}
	src := TomlConfig{NoColor: true}
	mergeToml(&dst, src)
	if !dst.NoColor {
		t.Error("NoColor should be true after merge")
	}
}

func TestResolveConfig_ThreeLayers(t *testing.T) {
	home := filepath.Join(t.TempDir(), "supercli-data")
	cwd := filepath.Join(home, "project")
	os.MkdirAll(filepath.Join(cwd, ".supercli"), 0o755)
	os.MkdirAll(home, 0o755)

	// Global config.
	globalToml := `
default_model = "gpt-4o-mini"
draft_mode = "off"
no_color = true
`
	os.WriteFile(filepath.Join(home, "config.toml"), []byte(globalToml), 0o644)

	// Project config overrides default_model.
	projectToml := `
default_model = "gpt-4o"
draft_mode = "critical"
`
	os.WriteFile(filepath.Join(cwd, ".supercli", "config.toml"), []byte(projectToml), 0o644)

	cfg, err := ResolveConfig(home, cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	// Project overrides global.
	if cfg.DefaultModel != "gpt-4o" {
		t.Errorf("DefaultModel = %q, want gpt-4o (project)", cfg.DefaultModel)
	}
	if cfg.DraftMode != "critical" {
		t.Errorf("DraftMode = %q, want critical (project)", cfg.DraftMode)
	}
	// Global preserved where project didn't set.
	if !cfg.NoColor {
		t.Error("NoColor should be true (from global)")
	}
}

func TestApplyTomlToConfig_FillsZeros(t *testing.T) {
	c := &Config{} // all zeros
	toml := TomlConfig{
		DefaultModel: "gpt-4o",
		Provider:     "openai",
		Debug:        true,
	}
	ApplyTomlToConfig(c, toml)
	if c.Model != "gpt-4o" {
		t.Errorf("Model = %q", c.Model)
	}
	if c.Provider != "openai" {
		t.Errorf("Provider = %q", c.Provider)
	}
	if !c.Debug {
		t.Error("Debug should be true")
	}
}

func TestApplyTomlToConfig_DoesNotOverrideEnv(t *testing.T) {
	c := &Config{
		Model:    "gpt-4o-mini",
		Provider: "echo",
	}
	toml := TomlConfig{
		DefaultModel: "gpt-4o",
		Provider:     "openai",
	}
	ApplyTomlToConfig(c, toml)
	// Env/flag values should not be overridden.
	if c.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want gpt-4o-mini (env wins)", c.Model)
	}
	if c.Provider != "echo" {
		t.Errorf("Provider = %q, want echo (env wins)", c.Provider)
	}
}

func TestApplyTomlToConfig_DefaultProviderAppliesCredentialsAsUnit(t *testing.T) {
	c := &Config{
		Provider: "openai",
		BaseURL:  "http://stale/v1",
		APIKey:   "stale-key",
		Model:    "deepseek-v4-flash",
	}
	toml := TomlConfig{
		DefaultProvider: "deepseek",
		Providers: []ProviderConf{{
			Name:    "deepseek",
			Type:    "openai",
			BaseURL: "https://api.deepseek.com/v1",
			APIKey:  "real-key",
		}},
	}
	ApplyTomlToConfig(c, toml)
	if c.Provider != "openai" || c.BaseURL != "https://api.deepseek.com/v1" || c.APIKey != "real-key" {
		t.Fatalf("config = %+v, want default provider credentials applied", c)
	}
}

func TestTomlConfigToEnv_SetsOnlyIfNotSet(t *testing.T) {
	os.Unsetenv("SUPERCLI_TOML_TEST_MODEL")
	TomlConfigToConfig := func(t TomlConfig) {
		TomlConfigToEnv(t)
	}
	TomlConfigToConfig(TomlConfig{DefaultModel: "gpt-4o"})
	if v := os.Getenv("SUPERCLI_TOML_TEST_MODEL"); v != "" {
		// Not testing this env var — just make sure the func doesn't panic.
	}

	// Test the actual function with a known env var.
	t.Setenv("SUPERCLI_LLM_MODEL", "already-set")
	TomlConfigToEnv(TomlConfig{DefaultModel: "should-not-override"})
	if v := os.Getenv("SUPERCLI_LLM_MODEL"); v != "already-set" {
		t.Errorf("env should not be overridden, got %q", v)
	}
}

func TestSaveToml_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", ".supercli", "config.toml")
	err := SaveToml(path, TomlConfig{DefaultModel: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("file should exist")
	}
}

func TestLoadToml_McpServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
[mcp.servers.context7]
command = "npx"
args = ["-y", "@upstash/context7-mcp"]

[mcp.servers.context7.env]
CONTEXT7_API_KEY = "secret"

[mcp.servers.echo]
command = "echo-server"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadToml(path)
	if err != nil {
		t.Fatalf("LoadToml: %v", err)
	}
	c7, ok := cfg.Mcp.Servers["context7"]
	if !ok {
		t.Fatalf("context7 missing: %+v", cfg.Mcp)
	}
	if c7.Command != "npx" || len(c7.Args) != 2 || c7.Env["CONTEXT7_API_KEY"] != "secret" {
		t.Errorf("context7 = %+v", c7)
	}
	if cfg.Mcp.Servers["echo"].Command != "echo-server" {
		t.Errorf("echo = %+v", cfg.Mcp.Servers["echo"])
	}

	// Merge: project entry overrides same-name global entry, keeps others.
	global := cfg
	project := TomlConfig{Mcp: McpConf{Servers: map[string]McpServerConf{
		"echo": {Command: "other"},
	}}}
	mergeToml(&global, project)
	if global.Mcp.Servers["echo"].Command != "other" {
		t.Errorf("merge override failed: %+v", global.Mcp.Servers["echo"])
	}
	if _, ok := global.Mcp.Servers["context7"]; !ok {
		t.Error("merge dropped context7")
	}
}
