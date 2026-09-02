//go:build windows

package webgui

import (
	"path/filepath"
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
