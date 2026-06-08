package sandbox

import (
	"strings"
	"testing"
)

func TestScrubEnv_DefaultKeep(t *testing.T) {
	env := []string{
		"HOME=/home/u",
		"PATH=/usr/bin",
		"USER=u",
		"OPENAI_API_KEY=sk-secret",
		"AWS_SECRET_KEY=secret",
		"GITHUB_TOKEN=ghp_secret",
		"DB_PASSWORD=hunter2",
		"FOO=bar",
		"RANDOM_VAR=42",
	}
	got := ScrubEnv(env)
	names := make(map[string]bool)
	for _, line := range got {
		eq := strings.IndexByte(line, '=')
		names[line[:eq]] = true
	}
	// Default-keep should be present.
	for _, want := range []string{"HOME", "PATH", "USER"} {
		if !names[want] {
			t.Errorf("expected %q in scrubbed env", want)
		}
	}
	// Secret patterns should be gone, even if the
	// name would normally be kept (defense in depth).
	for _, bad := range []string{"OPENAI_API_KEY", "AWS_SECRET_KEY", "GITHUB_TOKEN", "DB_PASSWORD"} {
		if names[bad] {
			t.Errorf("secret %q should be scrubbed", bad)
		}
	}
	// Unrelated vars should be gone.
	for _, bad := range []string{"FOO", "RANDOM_VAR"} {
		if names[bad] {
			t.Errorf("non-allowlisted %q should be scrubbed", bad)
		}
	}
}

func TestScrubEnv_EmptyEnv(t *testing.T) {
	got := ScrubEnv(nil)
	if got == nil {
		t.Error("ScrubEnv(nil) should not return nil")
	}
	// On the test machine at least PATH will be there.
	found := false
	for _, line := range got {
		if strings.HasPrefix(line, "PATH=") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected PATH in scrubbed env (os.Environ)")
	}
}

func TestScrubEnvWithExtra(t *testing.T) {
	env := []string{
		"HOME=/home/u",
		"GOPATH=/go",
		"OPENAI_API_KEY=sk-secret",
	}
	got := ScrubEnvWithExtra(env, "GOPATH")
	names := make(map[string]bool)
	for _, line := range got {
		eq := strings.IndexByte(line, '=')
		names[line[:eq]] = true
	}
	if !names["GOPATH"] {
		t.Error("GOPATH should be present with extraKeep")
	}
	if !names["HOME"] {
		t.Error("HOME should still be present")
	}
	if names["OPENAI_API_KEY"] {
		t.Error("OPENAI_API_KEY should be scrubbed even with extraKeep")
	}
}

func TestScrubEnv_NoEqualSign(t *testing.T) {
	env := []string{
		"HOME=/home/u",
		"NOEQUALS",
		"PATH=/bin",
	}
	got := ScrubEnv(env)
	if len(got) != 2 {
		t.Errorf("expected 2 entries, got %d: %v", len(got), got)
	}
}

func TestMatchesSecret(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"PATH", false},
		{"HOME", false},
		{"OPENAI_API_KEY", true},
		{"my_secret_token", true},   // case-insensitive
		{"AWS_ACCESS_KEY_ID", true}, // matches AWS_
		{"GITHUB_TOKEN", true},
		{"DBPASSWORD", true},
		{"RANDOM", false},
		{"LANG", false},
		{"AUTH_HEADER", true},
	}
	for _, c := range cases {
		if got := matchesSecret(c.name); got != c.want {
			t.Errorf("matchesSecret(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestScrubEnv_PreservesValues(t *testing.T) {
	env := []string{
		"HOME=/home/u",
		"PATH=/usr/local/bin:/usr/bin",
	}
	got := ScrubEnv(env)
	want := map[string]string{
		"HOME": "/home/u",
		"PATH": "/usr/local/bin:/usr/bin",
	}
	for _, line := range got {
		for k, v := range want {
			if strings.HasPrefix(line, k+"=") {
				if line != k+"="+v {
					t.Errorf("line %q has wrong value", line)
				}
				delete(want, k)
			}
		}
	}
	if len(want) > 0 {
		t.Errorf("missing vars: %v", want)
	}
}
