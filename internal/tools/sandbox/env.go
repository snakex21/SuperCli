package sandbox

import (
	"strings"
)

// defaultKeep is the standard allowlist of env vars
// that we preserve for sub-processes. Anything not in
// this list is dropped unless it matches one of the
// sensitive patterns (in which case it is dropped too).
var defaultKeep = []string{
	"HOME", "PATH", "PATHEXT", "LANG", "LC_ALL", "LC_CTYPE",
	"USER", "USERNAME", "USERPROFILE", "SHELL", "COMSPEC",
	"APPDATA", "LOCALAPPDATA", "SYSTEMROOT", "WINDIR",
	"TMPDIR", "TMP", "TEMP",
	"PWD", "OLDPWD",
	"TERM", "COLORTERM", "NO_COLOR",
	"EDITOR", "VISUAL",
	// Non-secret Go toolchain locations and cache controls. Go derives its
	// defaults from HOME on Unix and USERPROFILE/LOCALAPPDATA on Windows; both
	// platform families must survive scrubbing so ctx_execute can build/test.
	"GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT", "GOFLAGS", "GOTOOLCHAIN",
	"SUPERCLI_HOME", "SUPERCLI_DEBUG",
}

// secretPatterns is the substring match used to drop
// credentials. Matching is case-insensitive on the
// variable name.
var secretPatterns = []string{
	"KEY",      // covers API_KEY, PRIVATE_KEY, SSH_KEY, etc.
	"TOKEN",    // covers ACCESS_TOKEN, GITHUB_TOKEN, etc.
	"SECRET",   // covers CLIENT_SECRET, etc.
	"PASSWORD", // covers DB_PASSWORD, etc.
	"CREDENTIAL",
	"AUTH",
	"AWS_",    // AWS_ACCESS_KEY, AWS_SECRET_KEY
	"GITHUB_", // GITHUB_TOKEN
	"GITLAB_", // GITLAB_TOKEN
	"OPENAI_", // OPENAI_API_KEY
	"ANTHROPIC_",
	"GOOGLE_",
	"AZURE_",
}

// ScrubEnv returns a new []string suitable for
// os/exec.Command.Env. It starts from the current
// process environment (or env if non-nil), keeps only
// the default allowlist, and additionally drops any
// variable whose name contains a secret pattern.
//
// nil env means "use os.Environ()".
func ScrubEnv(env []string) []string {
	if env == nil {
		env = environ()
	}
	keep := make(map[string]bool, len(defaultKeep))
	for _, k := range defaultKeep {
		keep[k] = true
	}
	out := make([]string, 0, len(env))
	for _, line := range env {
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		name := line[:eq]
		if !keep[name] {
			continue
		}
		if matchesSecret(name) {
			continue
		}
		out = append(out, line)
	}
	return out
}

// ScrubEnvWithExtra behaves like ScrubEnv but lets
// the caller add more env names to the allowlist. Use
// this from tools that explicitly need, say, GOPATH or
// JAVA_HOME.
func ScrubEnvWithExtra(env []string, extraKeep ...string) []string {
	if len(extraKeep) == 0 {
		return ScrubEnv(env)
	}
	merged := make([]string, 0, len(defaultKeep)+len(extraKeep))
	merged = append(merged, defaultKeep...)
	merged = append(merged, extraKeep...)
	keep := make(map[string]bool, len(merged))
	for _, k := range merged {
		keep[k] = true
	}
	if env == nil {
		env = environ()
	}
	out := make([]string, 0, len(env))
	for _, line := range env {
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		name := line[:eq]
		if !keep[name] {
			continue
		}
		if matchesSecret(name) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func matchesSecret(name string) bool {
	upper := strings.ToUpper(name)
	for _, p := range secretPatterns {
		if strings.Contains(upper, p) {
			return true
		}
	}
	return false
}
