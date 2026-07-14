package prompt

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxProfileBytes = 4096

var unsafeProfileName = regexp.MustCompile(`[^a-z0-9._-]+`)

// ProfileFamily maps model identifiers to stable prompt-profile names. Exact
// model files still win, so users can tune one quant/model without affecting a
// whole family.
func ProfileFamily(model string) string {
	m := strings.ToLower(model)
	for _, f := range []string{"qwen", "deepseek", "llama", "mistral", "gemma", "phi", "gpt", "claude", "glm", "kimi", "mimo"} {
		if strings.Contains(m, f) {
			return f
		}
	}
	return "generic"
}

// LoadProfile reads at most one optional project profile from
// .supercli/prompts. Precedence: exact model, family, default. The bounded
// result is appended to the stable system prefix and costs zero extra calls.
func LoadProfile(home, model string) string {
	return LoadProfileAt(home, "", model)
}

// LoadProfileAt adds portable, installation-wide profiles from
// <dataDir>/profiles after project overrides. Only the first matching file is
// opened and its size is capped, so unused model families cost no I/O, memory,
// or prompt tokens.
func LoadProfileAt(home, dataDir, model string) string {
	exact := strings.Trim(unsafeProfileName.ReplaceAllString(strings.ToLower(strings.TrimSpace(model)), "-"), "-.")
	names := []string{}
	if exact != "" {
		names = append(names, exact+".md")
	}
	names = append(names, ProfileFamily(model)+".md", "default.md")
	roots := []string{filepath.Join(home, ".supercli", "prompts")}
	if strings.TrimSpace(dataDir) != "" {
		roots = append(roots, filepath.Join(dataDir, "profiles"))
	}
	for _, root := range roots {
		for _, name := range names {
			b, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				continue
			}
			if len(b) > maxProfileBytes {
				b = b[:maxProfileBytes]
				for len(b) > 0 && !utf8.Valid(b) {
					b = b[:len(b)-1]
				}
			}
			if text := strings.TrimSpace(string(b)); text != "" {
				return "Model profile (optional bounded instructions; lower priority than user request):\n" + text
			}
		}
	}
	return ""
}
