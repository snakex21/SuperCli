package context

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// EnvLoader picks a curated set of environment variables and
// formats them as a single source. It does NOT dump the entire
// environment — that would both bloat the context and leak
// secrets.
type EnvLoader struct {
	// Keys is the set of env var names to include. The default
	// is a small curated list safe for the model to see.
	Keys []string
}

// NewEnvLoader returns a loader with the default key set.
func NewEnvLoader() *EnvLoader {
	return &EnvLoader{Keys: defaultEnvKeys()}
}

func defaultEnvKeys() []string {
	return []string{
		"SHELL",
		"LANG",
		"TERM",
		"USER",
		"PATH", // truncated
		"SUPERCLI_HOME",
		"SUPERCLI_LLM_PROVIDER",
		"SUPERCLI_LLM_MODEL",
	}
}

// Name implements Loader.
func (l *EnvLoader) Name() string { return "env" }

// Priority is low; env is background context, not a primary signal.
func (l *EnvLoader) Priority() int { return 20 }

// Load returns a formatted env dump.
func (l *EnvLoader) Load() (Source, error) {
	keys := l.Keys
	if len(keys) == 0 {
		keys = defaultEnvKeys()
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := os.Getenv(k)
		if v == "" {
			continue
		}
		// Truncate PATH to first 3 components to keep tokens low.
		if k == "PATH" {
			parts := strings.Split(v, string(os.PathListSeparator))
			if len(parts) > 3 {
				v = strings.Join(parts[:3], string(os.PathListSeparator)) + "…"
			}
		}
		// Redact obvious secrets (defence in depth).
		if isSecretKey(k) {
			v = "***redacted***"
		}
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	body := strings.TrimRight(b.String(), "\n")
	if body == "" {
		return Source{Name: l.Name(), Body: ""}, nil
	}
	return Source{
		Name:     l.Name(),
		Body:     body,
		Priority: l.Priority(),
		TokenCap: 200,
	}, nil
}

// isSecretKey is intentionally conservative. Better to redact
// too much than too little.
func isSecretKey(k string) bool {
	lk := strings.ToLower(k)
	if strings.Contains(lk, "key") || strings.Contains(lk, "secret") || strings.Contains(lk, "token") || strings.Contains(lk, "password") {
		// But allow SUPERCLI_LLM_*_API_KEY only when the user
		// explicitly sets an env flag saying "ok to expose".
		return true
	}
	return false
}
