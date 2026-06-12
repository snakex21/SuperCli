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
