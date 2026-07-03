package agent

import "testing"

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
