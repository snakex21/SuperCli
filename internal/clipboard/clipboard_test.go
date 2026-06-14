package clipboard

import (
	"runtime"
	"strings"
	"testing"
)

func TestClipboardCommand_CurrentPlatform(t *testing.T) {
	name, _, err := clipboardCommand()
	switch runtime.GOOS {
	case "windows":
		if err != nil {
			t.Fatalf("windows should resolve a command, got err: %v", err)
		}
		if name != "clip" {
			t.Errorf("windows command = %q, want clip", name)
		}
	case "darwin":
		if err != nil {
			t.Fatalf("darwin should resolve a command, got err: %v", err)
		}
		if name != "pbcopy" {
			t.Errorf("darwin command = %q, want pbcopy", name)
		}
	case "linux":
		// May legitimately fail if neither wl-copy nor xclip is
		// installed; that is an acceptable, descriptive error.
		if err != nil && !strings.Contains(err.Error(), "no utility") {
			t.Errorf("linux error should mention missing utility, got: %v", err)
		}
	}
}

func TestRunClipboard_MissingBinaryErrors(t *testing.T) {
	err := runClipboard("supercli-no-such-clip-binary", nil, "hi")
	if err == nil {
		t.Fatal("want error for missing binary")
	}
	if !strings.Contains(err.Error(), "clipboard:") {
		t.Errorf("error should be prefixed clipboard:, got %v", err)
	}
}

// TestWriteText_RoundTrip is a live test: on Windows it writes to
// the real clipboard and reads it back via PowerShell. Skipped on
// other platforms and tolerant of headless CI where the clipboard
// is unavailable.
func TestWriteText_RoundTrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("round-trip clipboard test runs on windows only")
	}
	want := "supercli clipboard roundtrip 12345"
	if err := WriteText(want); err != nil {
		t.Skipf("clipboard unavailable in this environment: %v", err)
	}
	got, err := readClipboardWindowsForTest()
	if err != nil {
		t.Skipf("could not read back clipboard: %v", err)
	}
	if strings.TrimSpace(got) != want {
		t.Errorf("round-trip = %q, want %q", strings.TrimSpace(got), want)
	}
}
