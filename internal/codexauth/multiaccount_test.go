package codexauth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuthFilePathFor_DefaultMapsToClassic(t *testing.T) {
	dir := "/data"
	// default + empty + "default" all map to classic auth.json so
	// existing single-account setups are untouched.
	for _, label := range []string{"", "default", "DEFAULT", "  "} {
		got := AuthFilePathFor(dir, label)
		want := AuthFilePath(dir)
		if got != want {
			t.Errorf("label %q → %q, want classic %q", label, got, want)
		}
	}
}

func TestAuthFilePathFor_NamedAccount(t *testing.T) {
	got := AuthFilePathFor("/data", "praca")
	want := filepath.Join("/data", "auth-praca.json")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAuthFilePathFor_SanitizesLabel(t *testing.T) {
	// Path-traversal / separator attempts must never escape dataDir.
	cases := map[string]string{
		"../evil":      "auth-evil.json",
		"a/b":          "auth-ab.json",
		"Pra Ca!":      "auth-praca.json",
		"konto_2":      "auth-konto_2.json",
		"UPPER":        "auth-upper.json",
	}
	for in, wantName := range cases {
		got := AuthFilePathFor("/data", in)
		want := filepath.Join("/data", wantName)
		if got != want {
			t.Errorf("label %q → %q, want %q", in, got, want)
		}
		// Critical: the result must stay directly under /data.
		if filepath.Dir(got) != filepath.Clean("/data") {
			t.Errorf("label %q escaped dataDir: %q", in, got)
		}
	}
}

func TestAuthFilePathFor_AllInvalidCharsFallsBackToDefault(t *testing.T) {
	// A label that sanitizes to empty must not produce "auth-.json".
	got := AuthFilePathFor("/data", "///")
	if got != AuthFilePath("/data") {
		t.Errorf("all-invalid label → %q, want classic auth.json", got)
	}
}

func TestListAccounts_EmptyDir(t *testing.T) {
	labels, err := ListAccounts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 0 {
		t.Errorf("empty dir → %v, want none", labels)
	}
}

func TestListAccounts_MissingDir(t *testing.T) {
	labels, err := ListAccounts(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if labels != nil {
		t.Errorf("missing dir → %v, want nil", labels)
	}
}

func TestListAccounts_MixedFiles(t *testing.T) {
	dir := t.TempDir()
	// classic + two named + noise that must be ignored.
	mk := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mk("auth.json")
	mk("auth-praca.json")
	mk("auth-prywatne.json")
	mk("config.toml")       // ignored
	mk("auth-.json")        // malformed → ignored
	mk("notauth-x.json")    // ignored

	labels, err := ListAccounts(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"default": true, "praca": true, "prywatne": true}
	if len(labels) != len(want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}
	for _, l := range labels {
		if !want[l] {
			t.Errorf("unexpected label %q", l)
		}
	}
}

func TestListAccounts_RoundTripWithSave(t *testing.T) {
	dir := t.TempDir()
	// Save two accounts via the path helper, then list them.
	for _, label := range []string{"default", "work"} {
		af := &AuthFile{Tokens: &TokenData{AccessToken: "tok-" + label}}
		if err := Save(AuthFilePathFor(dir, label), af); err != nil {
			t.Fatalf("save %s: %v", label, err)
		}
	}
	labels, err := ListAccounts(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 2 {
		t.Fatalf("labels = %v, want 2", labels)
	}
	// And each loads back its own tokens.
	def, _ := Load(AuthFilePathFor(dir, "default"))
	if def == nil || def.Tokens.AccessToken != "tok-default" {
		t.Errorf("default account tokens wrong: %+v", def)
	}
	work, _ := Load(AuthFilePathFor(dir, "work"))
	if work == nil || work.Tokens.AccessToken != "tok-work" {
		t.Errorf("work account tokens wrong: %+v", work)
	}
}
