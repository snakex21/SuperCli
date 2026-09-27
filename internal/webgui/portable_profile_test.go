package webgui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortableAppWindowRejectsMissingOrBlockedProfileBeforeLaunch(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "file")
	if err := os.WriteFile(blocker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"", "   ", blocker, filepath.Join(blocker, "browser-profile")} {
		t.Run(profile, func(t *testing.T) {
			cmd, err := OpenAppWindow("http://127.0.0.1:1/", profile)
			if err == nil || cmd != nil || !strings.Contains(err.Error(), "portable browser profile") {
				t.Fatalf("expected profile error before browser launch, got cmd=%v err=%v", cmd, err)
			}
		})
	}
	if data, err := os.ReadFile(blocker); err != nil || string(data) != "keep" {
		t.Fatalf("profile error changed existing file: %q, %v", data, err)
	}
}

func TestPortableAppWindowProfileCreatesAndReusesLocalDirectory(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "data", "browser-profile")
	for attempt := 0; attempt < 2; attempt++ {
		resolved, err := prepareAppWindowProfile(profile)
		if err != nil {
			t.Fatal(err)
		}
		if resolved != profile || !filepath.IsAbs(resolved) {
			t.Fatalf("profile changed location: %q", resolved)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			t.Fatalf("missing profile directory: %v", err)
		}
		args := strings.Join(appWindowArgs("http://127.0.0.1:1/", resolved), "\n")
		if !strings.Contains(args, "--user-data-dir="+profile) {
			t.Fatalf("browser would lose its portable profile: %s", args)
		}
		if attempt == 0 {
			if err := os.WriteFile(filepath.Join(profile, "existing"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(profile, "existing")); err != nil || string(data) != "keep" {
		t.Fatalf("existing profile changed: %q, %v", data, err)
	}
}
