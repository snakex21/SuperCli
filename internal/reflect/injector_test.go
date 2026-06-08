package reflect

import (
	"context"
	"strings"
	"testing"
)

func newInjectorWithPatterns(t *testing.T, ps []Pattern) *Injector {
	t.Helper()
	rs, _, _ := newTestStore(t)
	for i := range ps {
		if err := rs.Save(context.Background(), &ps[i]); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	return &Injector{Store: rs}
}

func TestInjector_NilStore(t *testing.T) {
	inj := &Injector{Store: nil}
	out, err := inj.Build(context.Background(), "any system context")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if out != "" {
		t.Errorf("Build = %q, want empty for nil store", out)
	}
}

func TestInjector_NoPatterns(t *testing.T) {
	rs, _, _ := newTestStore(t)
	inj := &Injector{Store: rs}
	out, err := inj.Build(context.Background(), "context with words")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if out != "" {
		t.Errorf("Build = %q, want empty when no patterns", out)
	}
}

func TestInjector_BlankSystemContextReturnsEmpty(t *testing.T) {
	ps := []Pattern{samplePattern("11111111", "error", "search_code: rg missing")}
	inj := newInjectorWithPatterns(t, ps)
	out, _ := inj.Build(context.Background(), "")
	if out != "" {
		t.Errorf("Build = %q, want empty for blank system", out)
	}
}

func TestInjector_SelectsRelevantPattern(t *testing.T) {
	ps := []Pattern{
		samplePattern("11111111", "error", "search_code: rg missing"),
		samplePattern("22222222", "error", "image upload: png corrupt"),
	}
	ps[0].Description = "search_code failed with rg executable not found"
	ps[1].Description = "image upload tool returned corrupt png"
	inj := newInjectorWithPatterns(t, ps)
	out, err := inj.Build(context.Background(),
		"the user wants to search the codebase for search_code usage")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if out == "" {
		t.Fatal("Build returned empty, want a section")
	}
	if !strings.Contains(out, "search_code") {
		t.Errorf("output = %q, want to mention search_code", out)
	}
	if strings.Contains(out, "png") {
		t.Errorf("output = %q, want to drop unrelated png pattern", out)
	}
}

func TestInjector_MaxPatternsCap(t *testing.T) {
	ps := []Pattern{
		samplePattern("11111111", "error", "alpha: one"),
		samplePattern("22222222", "error", "beta: two"),
		samplePattern("33333333", "error", "gamma: three"),
	}
	// All match equally well.
	inj := newInjectorWithPatterns(t, ps)
	inj.MaxPatterns = 2
	out, _ := inj.Build(context.Background(), "alpha beta gamma delta")
	if out == "" {
		t.Fatal("Build empty")
	}
	// Count bullet lines.
	count := strings.Count(out, "\n- ")
	if count != 2 {
		t.Errorf("bullet count = %d, want 2 (MaxPatterns cap)", count)
	}
}

func TestInjector_HeadingAlwaysPresent(t *testing.T) {
	ps := []Pattern{samplePattern("11111111", "error", "alpha: matches")}
	inj := newInjectorWithPatterns(t, ps)
	out, _ := inj.Build(context.Background(), "alpha and beta")
	if !strings.HasPrefix(out, "## Relevant patterns") {
		t.Errorf("output = %q, want it to start with the heading", out)
	}
}

func TestInjector_MinScoreFilter(t *testing.T) {
	ps := []Pattern{samplePattern("11111111", "error", "specific: unrelated")}
	inj := newInjectorWithPatterns(t, ps)
	inj.MinScore = 0.9 // very strict
	out, _ := inj.Build(context.Background(), "alpha beta gamma")
	if out != "" {
		t.Errorf("output = %q, want empty when score < MinScore", out)
	}
}

func TestInjector_ContextCancel(t *testing.T) {
	ps := []Pattern{samplePattern("11111111", "error", "alpha: one")}
	inj := newInjectorWithPatterns(t, ps)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := inj.Build(ctx, "alpha")
	if err == nil {
		t.Error("Build on cancelled ctx returned nil err")
	}
}

func TestInjector_OutputIsSelfContained(t *testing.T) {
	ps := []Pattern{samplePattern("11111111", "error", "alpha: matches")}
	ps[0].Description = "alpha detail body"
	inj := newInjectorWithPatterns(t, ps)
	out, _ := inj.Build(context.Background(), "alpha beta gamma")
	// Each line is independent — no fenced code blocks
	// that would let patterns escape the bullet list.
	if strings.Contains(out, "```") {
		t.Errorf("output contains code fences: %q", out)
	}
}

func TestInjector_TruncatesLongDescription(t *testing.T) {
	ps := []Pattern{samplePattern("11111111", "error", "alpha: matches")}
	ps[0].Description = strings.Repeat("z", 500)
	inj := newInjectorWithPatterns(t, ps)
	out, _ := inj.Build(context.Background(), "alpha")
	if len(out) > 500 {
		t.Errorf("output = %d bytes, want truncated", len(out))
	}
}
