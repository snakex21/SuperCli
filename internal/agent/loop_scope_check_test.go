package agent

import (
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestScopeExtraFiles(t *testing.T) {
	prompt := "Make these changes and nothing else:\n1. In config/app.toml set `ttl_seconds` to 600.\n2. In app/cli.py change the default of --workers to 8."
	got := scopeExtraFiles(prompt, []string{"config/app.toml", `app\cli.py`, "app/settings.py", "tests/test_cli.py"})
	if want := []string{"app/settings.py", "tests/test_cli.py"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("extra = %q, want %q", got, want)
	}
	// Not scope-limited, or no file named: nothing to compare against.
	if got := scopeExtraFiles("In app/cli.py change the default to 8.", []string{"app/other.py"}); got != nil {
		t.Fatalf("unlimited request flagged: %q", got)
	}
	if got := scopeExtraFiles("Rename Foo to Bar and nothing else.", []string{"a.go"}); got != nil {
		t.Fatalf("request without files flagged: %q", got)
	}
	if got := scopeExtraFiles("Zmień tylko te pliki: `src/a.ts`.", []string{"src/a.ts"}); got != nil {
		t.Fatalf("named file flagged: %q", got)
	}
}

func TestScopeCheckReviewsOnceThenFinishes(t *testing.T) {
	// completionLoop edits f0.txt and f1.txt; the request names only f0.txt.
	l, main := completionLoop(t, false, 2, "Done.", "Restored f1.txt; only f0.txt changed.")
	l.scopeCheck = true
	runCompletionLoop(t, l, "Update f0.txt and nothing else.")
	if got := atomic.LoadInt32(&main.calls); got != 3 {
		t.Fatalf("main calls = %d, want 3 (edits, answer, reviewed answer)", got)
	}
	var reviews []string
	for _, m := range l.Messages {
		if strings.HasPrefix(m.Content, "[scope check]") {
			reviews = append(reviews, m.Content)
		}
	}
	if len(reviews) != 1 || !strings.Contains(reviews[0], "also changed: f1.txt.") {
		t.Fatalf("scope review = %q", reviews)
	}
	// Off: the same Run ends on its first answer.
	l, main = completionLoop(t, false, 2, "Done.")
	runCompletionLoop(t, l, "Update f0.txt and nothing else.")
	if got := atomic.LoadInt32(&main.calls); got != 2 {
		t.Fatalf("scope_check off: main calls = %d, want 2", got)
	}
}
