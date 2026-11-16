package agent

import (
	"context"
	"testing"

	"supercli/internal/tools"
)

func TestWebReferenceForPrompt(t *testing.T) {
	cases := []struct {
		prompt   string
		web, url bool
	}{
		{"Fix the off-by-one bug in paginate.ts", false, false},
		{"Rename the field and run the tests", false, false},
		{"Summarize https://example.org/post", true, true},
		{"Find the latest release notes online", true, false},
		{"What are today's news about Go 1.27?", true, false},
		{"Sprawdź w internecie najnowszą wersję", true, false},
	}
	for _, c := range cases {
		web, url := webReferenceForPrompt(c.prompt)
		if web != c.web || url != c.url {
			t.Errorf("%q: web=%v url=%v, want %v %v", c.prompt, web, url, c.web, c.url)
		}
	}
}

// web_lookup is not thin core: an ordinary coding turn does not carry its
// schema, a web turn gets it (and web_fetch for a URL) in the request tail,
// and the cacheable tools prefix is the same for both.
func TestThinCoreLeavesWebToolsToWebTurns(t *testing.T) {
	l := downloadRequestedFixture(t, true)
	l.registry.MustRegister(tools.NewWebSearch("", "").LookupSpec())
	l.registry.MarkAlwaysOn("web_lookup")
	l.registry.MustRegister(tools.NewWebFetch().Spec())
	if isThinCore("web_lookup") {
		t.Fatal("web_lookup is thin core again")
	}
	l.prepareRunRoute(context.Background(), "fix the failing test in parser.go")
	ordinary := l.buildToolDefs()
	if containsNativeContract(ordinary, "web_lookup") || containsNativeContract(ordinary, "web_fetch") {
		t.Fatal("ordinary turn carries web schemas")
	}
	plain, _ := l.prepareProviderMessages(true)
	if requestContainsToolContract(plain, "web_lookup", ordinary) {
		t.Fatal("ordinary turn carries a web_lookup contract")
	}
	l.prepareRunRoute(context.Background(), "read https://example.org/changelog and summarize it")
	webDefs := l.buildToolDefs()
	web, _ := l.prepareProviderMessages(true)
	if !requestContainsToolContract(web, "web_lookup", webDefs) || !requestContainsToolContract(web, "web_fetch", webDefs) {
		t.Fatal("web turn lacks the web_lookup/web_fetch contracts")
	}
	if len(ordinary) != len(webDefs) {
		t.Fatal("web turn changed the cacheable tools prefix")
	}
}
