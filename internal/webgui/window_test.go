package webgui

import (
	"runtime"
	"strings"
	"testing"
)

func TestChromiumCandidates_NonEmpty(t *testing.T) {
	got := chromiumCandidates()
	if len(got) == 0 {
		t.Fatalf("no candidates for GOOS=%s", runtime.GOOS)
	}
	// Every candidate must be a non-empty executable name or path.
	for i, c := range got {
		if c == "" {
			t.Errorf("candidate %d is empty", i)
		}
	}
}

func TestAppWindowArgsUseDedicatedProfile(t *testing.T) {
	args := appWindowArgs("http://127.0.0.1:1234/", `C:\data\browser-profile`)
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"--app=http://127.0.0.1:1234/",
		`--user-data-dir=C:\data\browser-profile`,
		"--disable-background-mode",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("app args %q do not contain %q", args, want)
		}
	}
}

func TestErrNoBrowser_Message(t *testing.T) {
	if errNoBrowser == nil {
		t.Fatal("errNoBrowser must be defined")
	}
	if errNoBrowser.Error() == "" {
		t.Error("errNoBrowser has empty message")
	}
}
