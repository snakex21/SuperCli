//go:build windows

package session

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
	"supercli/internal/llm"
)

func TestSessionImagePublicationLockedIncompleteFailsWithoutLeaks(t *testing.T) {
	data := []byte("complete bytes remain inline if locked publication fails")
	w, path := sessionImagePublicationFixture(t, data)
	if err := os.WriteFile(path, data[:3], 0600); err != nil {
		t.Fatal(err)
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || !info.Mode().IsRegular() || info.Size() != 3 {
		_ = windows.CloseHandle(handle)
		t.Fatalf("locked fixture must reach publication: %v", statErr)
	}
	ref, publicationErr := w.ExternalizeImage(context.Background(), "image/png", data)
	if err := windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	if publicationErr == nil || !reflect.DeepEqual(ref, llm.ImageRef{}) {
		t.Fatalf("locked incomplete image accepted: %+v / %v", ref, publicationErr)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data[:3]) {
		t.Fatal("failed replace modified the locked final")
	}
	assertNoSessionImageTemporaries(t, w)
	ref, err = w.ExternalizeImage(context.Background(), "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(ref.Path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("later retry failed to repair unlocked partial image")
	}
	assertNoSessionImageTemporaries(t, w)
}

func TestSessionImagePublicationWindowsLongPortablePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), strings.Repeat("portable", 8), strings.Repeat("session", 8), strings.Repeat("media", 12))
	w := NewWriter(&Store{root: root}, "long-portable-path")
	data := []byte("pixels under a long portable data directory")
	ref, err := w.ExternalizeImage(context.Background(), "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(ref.Path) < 260 {
		t.Fatalf("fixture path not long: %d", len(ref.Path))
	}
	got, err := os.ReadFile(ref.Path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("long-path bytes changed: %v", err)
	}
	again, err := w.ExternalizeImage(context.Background(), "image/png", data)
	if err != nil || again != ref {
		t.Fatalf("long-path dedup changed: %+v / %v", again, err)
	}
	assertNoSessionImageTemporaries(t, w)
}
