package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPortableMigrationLeavesLegacySourceUnchanged(t *testing.T) {
	for _, existingMarker := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-marker", true: "existing-marker"}[existingMarker], func(t *testing.T) {
			root := t.TempDir()
			profile := filepath.Join(root, "legacy-profile")
			t.Setenv("USERPROFILE", profile)
			t.Setenv("HOME", profile)
			old := filepath.Join(profile, ".supercli")
			if err := os.MkdirAll(filepath.Join(old, "sessions"), 0700); err != nil {
				t.Fatal(err)
			}
			originals := map[string]string{
				"config.toml":          "model = \"fixture\"\n",
				"sessions/example.txt": "saved conversation",
			}
			projectPath := filepath.Join(root, "user-project")
			projects, err := json.Marshal(map[string]string{
				filepath.Join(old, "workspace"): filepath.Join(old, "sessions"),
				projectPath:                     projectPath,
			})
			if err != nil {
				t.Fatal(err)
			}
			originals["projects.json"] = string(projects)
			if existingMarker {
				originals["MOVED.txt"] = "existing user-owned migration receipt"
			}
			for rel, content := range originals {
				if err := os.WriteFile(filepath.Join(old, filepath.FromSlash(rel)), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			dataDir := filepath.Join(root, "application", "supercli-data")
			message, err := migrateLegacyData(dataDir)
			if err != nil || message == "" {
				t.Fatalf("migration: message=%q err=%v", message, err)
			}
			if strings.Contains(message, "see MOVED.txt") {
				t.Error("migration message advertises a receipt written into the old profile")
			}
			actual := map[string]string{}
			if err := filepath.WalkDir(old, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				rel, err := filepath.Rel(old, path)
				if err != nil {
					return err
				}
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				actual[filepath.ToSlash(rel)] = string(body)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, originals) {
				t.Error("migration added or modified files in the old profile")
			}
			for rel, content := range originals {
				if rel == "projects.json" {
					continue
				}
				body, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
				if err != nil || string(body) != content {
					t.Fatalf("portable copy lost %s: %v", rel, err)
				}
			}
			body, err := os.ReadFile(filepath.Join(dataDir, "projects.json"))
			if err != nil {
				t.Fatal(err)
			}
			var migrated map[string]string
			if err := json.Unmarshal(body, &migrated); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{
				filepath.Join(dataDir, "workspace"): filepath.Join(dataDir, "sessions"),
				projectPath:                         projectPath,
			}
			if !reflect.DeepEqual(migrated, want) {
				t.Fatalf("wrong relocated references: got %v want %v", migrated, want)
			}
			message, err = migrateLegacyData(dataDir)
			if err != nil || message != "" {
				t.Fatalf("existing portable data was migrated again: %q %v", message, err)
			}
		})
	}
}

func TestPortableMigrationKeepsExistingDestination(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "legacy-profile")
	t.Setenv("USERPROFILE", profile)
	t.Setenv("HOME", profile)
	old := filepath.Join(profile, ".supercli")
	dataDir := filepath.Join(root, "application", "supercli-data")
	for _, dir := range []string{old, dataDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(dir), 0600); err != nil {
			t.Fatal(err)
		}
	}
	message, err := migrateLegacyData(dataDir)
	if err != nil || message != "" {
		t.Fatalf("unexpected migration: %q %v", message, err)
	}
	for _, dir := range []string{old, dataDir} {
		body, err := os.ReadFile(filepath.Join(dir, "config.toml"))
		if err != nil || string(body) != dir {
			t.Fatalf("existing data changed: %q %v", dir, err)
		}
	}
}
