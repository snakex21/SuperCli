package render

import (
	"testing"

	"tmplsvc/config"
)

func TestRenderWithOverrides(t *testing.T) {
	c := config.Merge(config.Base(), config.Config{Vars: map[string]string{"lang": "pl"}})
	got := Render(c, "home", "{{brand}}/{{page}}/{{lang}}/{{missing}}")
	if got != "Acme/home/pl/{{missing}}" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderWithBaseOnly(t *testing.T) {
	c := config.Merge(config.Base(), config.Config{Name: "docs"})
	got := Render(c, "about", "{{brand}} {{year}} {{page}} {{lang}}")
	if got != "Acme 2026 about en" {
		t.Fatalf("got %q", got)
	}
}
