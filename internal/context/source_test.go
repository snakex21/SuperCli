package context

import (
	"testing"
)

func TestSource_EstimateTokens(t *testing.T) {
	s := MustSource("x", "hello world") // 11 chars
	if s.EstimateTokens() != 3 {
		t.Errorf("got %d", s.EstimateTokens())
	}
	s = MustSource("x", "")
	if s.EstimateTokens() != 0 {
		t.Errorf("empty: got %d", s.EstimateTokens())
	}
}

func TestSources_Append_ReplacesByName(t *testing.T) {
	s := NewSources()
	s.Append(MustSource("a", "1"))
	s.Append(MustSource("b", "2"))
	s.Append(MustSource("a", "11"))
	if s.Len() != 2 {
		t.Fatalf("Len = %d", s.Len())
	}
	if got, _ := s.Get("a"); got.Body != "11" {
		t.Fatalf("a = %q", got.Body)
	}
}

func TestSources_TotalTokens(t *testing.T) {
	s := NewSources()
	s.Append(MustSource("a", "abcdefgh"))  // 8 chars → 2 tokens
	s.Append(MustSource("b", "abcdefghij")) // 10 chars → 3 tokens (rounded up)
	if got := s.TotalTokens(); got != 5 {
		t.Errorf("TotalTokens = %d, want 5", got)
	}
}

func TestSources_FitToBudget_KeepsHighestPriority(t *testing.T) {
	s := NewSources()
	s.Append(Source{Name: "low", Body: "1234", Priority: 10})
	s.Append(Source{Name: "high", Body: "1234", Priority: 100})
	s.Append(Source{Name: "med", Body: "1234", Priority: 50})
	// Each is 1 token. Budget = 2.
	fit := s.FitToBudget(2)
	if fit.Len() != 2 {
		t.Fatalf("Len = %d, want 2", fit.Len())
	}
	names := fit.Names()
	// Expect high + med kept, low dropped.
	if names[0] != "high" || names[1] != "med" {
		t.Errorf("names = %v, want [high med]", names)
	}
}

func TestSources_FitToBudget_ZeroMaxReturnsAll(t *testing.T) {
	s := NewSources()
	s.Append(MustSource("a", "1234"))
	s.Append(MustSource("b", "5678"))
	fit := s.FitToBudget(0)
	if fit.Len() != 2 {
		t.Errorf("Len = %d, want 2", fit.Len())
	}
}

func TestSources_FitToBudget_FitsExactly(t *testing.T) {
	s := NewSources()
	s.Append(Source{Name: "a", Body: "1234", Priority: 10}) // 1 token
	s.Append(Source{Name: "b", Body: "12345678", Priority: 10}) // 2 tokens
	// Total 3 tokens, budget 3 → keep all.
	fit := s.FitToBudget(3)
	if fit.Len() != 2 {
		t.Errorf("Len = %d, want 2 (no drop needed)", fit.Len())
	}
}

func TestSources_FitToBudget_DoesNotMutateOriginal(t *testing.T) {
	s := NewSources()
	s.Append(Source{Name: "a", Body: "1234", Priority: 10})
	s.Append(Source{Name: "b", Body: "1234", Priority: 100})
	_ = s.FitToBudget(1)
	if s.Len() != 2 {
		t.Errorf("original mutated, len = %d", s.Len())
	}
}

func TestSources_StaleTracking(t *testing.T) {
	s := NewSources()
	s.Append(MustSource("a", "x"))
	s.MarkStale("a")
	got, _ := s.Get("a")
	if !got.Stale {
		t.Fatal("Stale should be true")
	}
	stale := s.StaleNames()
	if len(stale) != 1 || stale[0] != "a" {
		t.Errorf("stale = %v, want [a]", stale)
	}
}

func TestSources_Render(t *testing.T) {
	s := NewSources()
	s.Append(MustSource("first", "hello"))
	s.Append(MustSource("second", "world"))
	out, tokens := s.Render(0)
	if !contains(out, "## first") || !contains(out, "hello") {
		t.Errorf("missing first: %q", out)
	}
	if !contains(out, "## second") || !contains(out, "world") {
		t.Errorf("missing second: %q", out)
	}
	if tokens == 0 {
		t.Errorf("tokens = 0")
	}
}

func TestSources_Render_AppliesBudget(t *testing.T) {
	s := NewSources()
	s.Append(Source{Name: "small", Body: "hi", Priority: 100})
	s.Append(Source{Name: "big", Body: "abcdefghijklmnopqrstuvwxyz", Priority: 10})
	out, _ := s.Render(2) // very small budget
	if !contains(out, "small") {
		t.Errorf("render dropped the high-priority source: %q", out)
	}
}

func TestSources_Replace(t *testing.T) {
	s := NewSources()
	s.Append(MustSource("a", "1"))
	s.Append(MustSource("b", "2"))
	if err := s.Replace(MustSource("c", "3")); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 3 {
		t.Errorf("Len = %d", s.Len())
	}
}

func TestSources_Order_PreservedOnReplace(t *testing.T) {
	s := NewSources()
	s.Append(MustSource("a", "1"))
	s.Append(MustSource("b", "2"))
	s.Append(MustSource("c", "3"))
	s.Replace(MustSource("b", "22"))
	names := s.Names()
	if names[0] != "a" || names[1] != "b" || names[2] != "c" {
		t.Errorf("order broken: %v", names)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
