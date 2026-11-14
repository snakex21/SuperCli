package theme

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// cssBlock returns the declarations of the first rule whose selector is sel.
func cssBlock(t *testing.T, css, sel string) map[string]string {
	t.Helper()
	at := strings.Index(css, sel+" {")
	if at < 0 {
		t.Fatalf("app.css has no %s block", sel)
	}
	body := css[at+len(sel)+2:]
	body = body[:strings.Index(body, "}")]
	decls := map[string]string{}
	for _, m := range regexp.MustCompile(`(--[\w-]+)\s*:\s*([^;]+);`).FindAllStringSubmatch(body, -1) {
		decls[m[1]] = strings.ToLower(strings.TrimSpace(m[2]))
	}
	return decls
}

// The GUI stylesheet and the TUI palette must declare the same values.
func TestGUITokensMatchSharedTokens(t *testing.T) {
	data, err := os.ReadFile("../../webgui/assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(data), "")
	dark := cssBlock(t, css, ":root")
	light := cssBlock(t, css, `:root[data-theme="light"]`)
	for name, pair := range Default.CSSVars() {
		if got := dark[name]; got != strings.ToLower(pair.Dark) {
			t.Errorf("dark %s = %q, shared token %q", name, got, pair.Dark)
		}
		if got := light[name]; got != strings.ToLower(pair.Light) {
			t.Errorf("light %s = %q, shared token %q", name, got, pair.Light)
		}
	}
}
