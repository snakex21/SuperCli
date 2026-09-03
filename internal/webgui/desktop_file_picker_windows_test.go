//go:build windows

package webgui

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestParseOpenFileNameBufferSingleFile(t *testing.T) {
	buffer := append(utf16.Encode([]rune(`C:\docs\raport.pdf`)), 0, 0)
	got := parseOpenFileNameBuffer(buffer)
	if len(got) != 1 || got[0] != `C:\docs\raport.pdf` {
		t.Fatalf("paths = %#v", got)
	}
}

func TestParseOpenFileNameBufferMultipleFiles(t *testing.T) {
	buffer := make([]uint16, 0, 64)
	for _, part := range []string{`C:\docs`, `a.docx`, `b.pdf`} {
		buffer = append(buffer, utf16.Encode([]rune(part))...)
		buffer = append(buffer, 0)
	}
	buffer = append(buffer, 0)
	got := parseOpenFileNameBuffer(buffer)
	want := []string{filepath.Join(`C:\docs`, `a.docx`), filepath.Join(`C:\docs`, `b.pdf`)}
	if len(got) != len(want) {
		t.Fatalf("paths = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("paths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// The common-dialog filter is a UTF-16 buffer consumed by Windows, so it is one
// of the two GUI strings the browser catalog cannot reach. Both variants must
// therefore exist in Go, and the UI language must actually select between them.
func TestPickerFilterFollowsTheUILanguage(t *testing.T) {
	english := string(utf16.Decode(pickerFilterUTF16(false)))
	polish := string(utf16.Decode(pickerFilterUTF16(true)))
	if !strings.Contains(english, "Supported files") || !strings.Contains(english, "All files") {
		t.Errorf("English picker filter is not English: %q", english)
	}
	if strings.Contains(english, "Obsługiwane") {
		t.Errorf("English picker filter leaked Polish labels: %q", english)
	}
	if !strings.Contains(polish, "Obsługiwane pliki") || !strings.Contains(polish, "Wszystkie pliki") {
		t.Errorf("Polish picker filter lost its labels: %q", polish)
	}
	// The extension patterns are language-independent and must not vary.
	if strings.Count(english, "*.png") != strings.Count(polish, "*.png") {
		t.Error("picker filter patterns differ between languages")
	}
}
