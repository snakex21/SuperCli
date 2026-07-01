package webgui

import (
	"runtime"
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

func TestErrNoBrowser_Message(t *testing.T) {
	if errNoBrowser == nil {
		t.Fatal("errNoBrowser must be defined")
	}
	if errNoBrowser.Error() == "" {
		t.Error("errNoBrowser has empty message")
	}
}
