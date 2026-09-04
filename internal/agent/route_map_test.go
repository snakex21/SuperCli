package agent

import (
	"strings"
	"testing"
)

func TestRouteMapClassify(t *testing.T) {
	m := DefaultRouteMap()
	cases := []struct {
		prompt string
		want   RouteMode
	}{
		{"cześć", RouteChatOnly},
		{"lubisz mnie?", RouteChatOnly},
		{"a dobrze", RouteChatOnly},
		{"co tutaj jest?", RouteCoordinator},
		{"napraw błąd w read_lines", RouteCoordinator},
		{"uruchom go test", RouteCoordinator},
	}
	for _, tc := range cases {
		if got := m.Classify(tc.prompt); got != tc.want {
			t.Fatalf("Classify(%q)=%s, want %s", tc.prompt, got, tc.want)
		}
	}
}

func TestRouteMapClassifyConfident(t *testing.T) {
	m := DefaultRouteMap()
	cases := []struct {
		prompt        string
		wantMode      RouteMode
		wantConfident bool
	}{
		// Explicit coordinator keyword hits — confident.
		{"napraw błąd w read_lines", RouteCoordinator, true},
		{"uruchom go test", RouteCoordinator, true},
		// Strong conceptual prefixes skip the navigator model.
		{"wyjaśnij jak działa rekursja", RouteAdvisor, true},
		{"what is speculative decoding?", RouteAdvisor, true},
		// Project evidence wins before the advisor prefix.
		{"wyjaśnij ten kod w pliku main.go", RouteCoordinator, true},
		// Explicit chat exact / prefix — confident.
		{"cześć", RouteChatOnly, true},
		{"lubisz mnie?", RouteChatOnly, true},
		// Ambiguous: falls through to coordinator default, NOT confident
		// (this is where the model navigator earns its round-trip).
		{"co lepsze na dłuższą metę?", RouteCoordinator, false},
		{"", RouteCoordinator, false},
	}
	for _, tc := range cases {
		mode, confident := m.ClassifyConfident(tc.prompt)
		if mode != tc.wantMode || confident != tc.wantConfident {
			t.Errorf("ClassifyConfident(%q) = (%s,%v), want (%s,%v)",
				tc.prompt, mode, confident, tc.wantMode, tc.wantConfident)
		}
	}
}

func TestImplementationVerificationHintOnlyForMutationWork(t *testing.T) {
	for _, prompt := range []string{"napraw ten błąd", "zaimplementuj panel", "refactor this function"} {
		if got := implementationVerificationHint(prompt); got == "" {
			t.Errorf("mutation prompt %q did not get verification contract", prompt)
		}
	}
	for _, prompt := range []string{"cześć", "co sądzisz o projekcie?", "wyjaśnij jak działa cache"} {
		if got := implementationVerificationHint(prompt); got != "" {
			t.Errorf("read-only prompt %q got verification contract %q", prompt, got)
		}
	}
}

// TestToollessPromptsOfferAnExit guards the actionable dead end: when the
// advisor/chat routes refuse project work, the wording must hand the user a
// concrete way out instead of naming a mode they cannot reach.
func TestToollessPromptsOfferAnExit(t *testing.T) {
	for name, prompt := range map[string]string{
		"advisor": advisorSystemPrompt,
		"chat":    chatOnlySystemPrompt,
	} {
		for _, want := range []string{"naming the file or repo", `navigator = "off"`} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s prompt lost its exit hint %q", name, want)
			}
		}
	}
}
