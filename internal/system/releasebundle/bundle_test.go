package releasebundle

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseBundleContainsPublicFilesAndNeverUserData(t *testing.T) {
	root := t.TempDir()
	output := t.TempDir()
	files := map[string]string{"README.md": "public", "LICENSE": "MIT", "THIRD_PARTY_NOTICES.md": "notices", "cli.exe": "cli", "gui.exe": "gui", "docs/readme/en.md": "English", "docs/readme/bg.md": "Bulgarian", "docs/configuration.md": "config docs", "docs/releases/1.0.0.md": "release notes", "docs/screenshots/1.0.0/README.md": "gallery", "docs/screenshots/1.0.0/gui-en.jpg": "public screenshot", "supercli-data/skills/builtin-skills.zip": "builtin skills", "supercli-data/config.toml": "SECRET", "supercli-data/auth.json": "SECRET", "supercli-data/sessions.db": "SECRET", "docs/evals/private.md": "SECRET", "notes.txt": "SECRET"}
	for name, raw := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := Pack(Options{Version: "1.0.0", OS: "windows", Arch: "amd64", CLI: filepath.Join(root, "cli.exe"), GUI: filepath.Join(root, "gui.exe"), Source: root, Output: output})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	names := map[string]bool{}
	for _, file := range archive.File {
		names[file.Name] = true
		if strings.Contains(file.Name, "auth") || strings.Contains(file.Name, "sessions") || file.Name == "supercli-data/config.toml" || strings.Contains(file.Name, "evals") || file.Name == "notes.txt" {
			t.Fatalf("private asset %s", file.Name)
		}
	}
	for _, name := range []string{"supercli.exe", "supercli-web.exe", "README.md", "docs/readme/en.md", "docs/readme/bg.md", "docs/releases/1.0.0.md", "docs/screenshots/1.0.0/README.md", "docs/screenshots/1.0.0/gui-en.jpg", "supercli-data/skills/builtin-skills.zip"} {
		if !names[name] {
			t.Fatalf("missing %s", name)
		}
	}
	manifest, sums, err := Manifest("1.0.0", output)
	if err != nil || len(manifest.Assets) != 1 {
		t.Fatalf("%+v %v", manifest, err)
	}
	raw, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	if manifest.Assets[0].SHA256 != digest || manifest.Assets[0].Size != int64(len(raw)) || !strings.Contains(sums, digest) {
		t.Fatal("manifest does not verify exact ZIP")
	}
	if _, err = Pack(Options{Version: "1.0.0", OS: "windows", Arch: "amd64", CLI: filepath.Join(root, "cli.exe"), GUI: filepath.Join(root, "gui.exe"), Source: root, Output: output}); err == nil {
		t.Fatal("pack silently overwrote an existing release")
	}
	if _, _, err = Manifest("1.0.1", output); err == nil {
		t.Fatal("stale bundle accepted for another release")
	}
}
