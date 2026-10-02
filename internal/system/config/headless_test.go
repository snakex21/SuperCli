package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHeadlessScopeOnlyReadsPortableGlobalConfig(t *testing.T) {
	if _, err := LoadHeadless(""); err == nil {
		t.Fatal("empty data directory accepted")
	}
	data, project := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".supercli"), 0700); err != nil {
		t.Fatal(err)
	}
	text := []byte("[headless.targets.project]\nprotocol='webdriver'\nendpoint='http://127.0.0.1:9515'\nallowed_actions=['open']\n")
	if err := os.WriteFile(filepath.Join(project, ".supercli", "config.toml"), text, 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	cfg, err := LoadHeadless(data)
	if err != nil || len(cfg.Targets) != 0 {
		t.Fatalf("project granted scope: %+v %v", cfg, err)
	}
	if err := os.WriteFile(filepath.Join(data, "config.toml"), text, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadHeadless(data)
	if err != nil || len(cfg.Targets) != 1 {
		t.Fatalf("global config missing: %+v %v", cfg, err)
	}
}
