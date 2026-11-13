package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/system/config"
)

func TestEnsureClaudeProviderPreservesExistingProvidersAndSelection(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)
	if name, err := mgr.EnsureClaudeProvider(); err != nil || name != "" {
		t.Fatalf("registered without login: %q / %v", name, err)
	}
	if err := config.SaveToml(filepath.Join(dir, "config.toml"), config.TomlConfig{
		DefaultProvider: "claude", DefaultModel: "existing-model",
		Providers: []config.ProviderConf{{Name: "claude", Type: config.ProviderAnthropic, BaseURL: "https://api.example.test/v1", APIKey: "existing-key"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude-auth.json"), []byte(`{"access_token":"oauth-secret","refresh_token":"refresh-secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	mgr.Reload()
	name, err := mgr.EnsureClaudeProvider()
	if err != nil || name != "claude-2" {
		t.Fatalf("provider collision: %q / %v", name, err)
	}
	if again, err := mgr.EnsureClaudeProvider(); err != nil || again != name {
		t.Fatalf("duplicate registration: %q / %v", again, err)
	}
	saved, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil || strings.Contains(string(saved), "oauth-secret") || strings.Contains(string(saved), "refresh-secret") {
		t.Fatal("OAuth tokens leaked to config")
	}
	tc, err := config.LoadToml(filepath.Join(dir, "config.toml"))
	if err != nil || tc.DefaultProvider != "claude" || tc.DefaultModel != "existing-model" || tc.Providers[0].APIKey != "existing-key" {
		t.Fatal("existing settings overwritten")
	}
	if err := mgr.SetDisabled(name, true); err != nil {
		t.Fatal(err)
	}
	if again, err := mgr.EnsureClaudeProvider(); err != nil || again != name {
		t.Fatal("disabled provider replaced")
	}
}

func TestClaudeScanRejectsCustomDestinationBeforeSendingCredentials(t *testing.T) {
	mgr := NewManager(t.TempDir())
	if err := mgr.Add("claude", config.ProviderClaude, "https://proxy.invalid/v1", "", ""); err != nil {
		t.Fatal(err)
	}
	res := mgr.ScanProvider("claude", llm.NewCapabilityRegistry())
	if res.Err == nil || !strings.Contains(res.Err.Error(), "requires https://api.anthropic.com/v1") {
		t.Fatalf("custom destination: %v", res.Err)
	}
}
