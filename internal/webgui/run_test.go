package webgui

import "testing"

func TestLocalLaunchURL(t *testing.T) {
	tests := map[string]string{
		"0.0.0.0:8765":  "http://127.0.0.1:8765/",
		"[::]:8765":     "http://[::1]:8765/",
		"127.0.0.1:12":  "http://127.0.0.1:12/",
		"[::1]:34":      "http://[::1]:34/",
		"localhost:567": "http://localhost:567/",
	}
	for in, want := range tests {
		if got := localLaunchURL(in); got != want {
			t.Errorf("localLaunchURL(%q) = %q, want %q", in, got, want)
		}
	}
}
