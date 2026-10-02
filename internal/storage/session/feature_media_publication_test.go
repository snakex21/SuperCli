package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"supercli/internal/llm"
	"testing"
)

func sessionImagePublicationFixture(t *testing.T, data []byte) (*Writer, string) {
	t.Helper()
	s := &Store{root: t.TempDir()}
	sessionID := "publication-fixture"
	dir := s.sessionMediaDir(sessionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return NewWriter(s, sessionID), filepath.Join(dir, fmt.Sprintf("%x.png", sum[:]))
}

func TestSessionImagePublicationRepairsIncomplete(t *testing.T) {
	data := []byte("complete publication bytes")
	for _, broken := range [][]byte{nil, data[:5], append(append([]byte(nil), data...), byte('x'))} {
		t.Run(fmt.Sprintf("length%d", len(broken)), func(t *testing.T) {
			w, path := sessionImagePublicationFixture(t, data)
			if err := os.WriteFile(path, broken, 0600); err != nil {
				t.Fatal(err)
			}
			ref, err := w.ExternalizeImage(context.Background(), "image/png", data)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(ref.Path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("returned successful ref for %d bytes; want complete %d bytes", len(got), len(data))
			}
		})
	}
}

// An open final-name file models the old first publisher between Create and Write.
// No scheduler timing or large writes are needed to reproduce the second caller.
func TestSessionImagePublicationDoesNotReturnInFlightFile(t *testing.T) {
	data := []byte("complete in-flight publication bytes")
	w, path := sessionImagePublicationFixture(t, data)
	first, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	done := make(chan error, 1)
	go func() {
		ref, err := w.ExternalizeImage(context.Background(), "image/png", data)
		if err != nil {
			done <- nil
			return
		} // Safe storage failure keeps original pixels inline.
		got, err := os.ReadFile(ref.Path)
		if err == nil && !bytes.Equal(got, data) {
			err = fmt.Errorf("second caller returned %d bytes before first publisher wrote %d", len(got), len(data))
		}
		done <- err
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	assertNoSessionImageTemporaries(t, w)
}

func TestSessionImagePublicationConcurrentComplete(t *testing.T) {
	for _, setup := range []string{"fresh", "partial", "fresh-independent-stores"} {
		t.Run(setup, func(t *testing.T) {
			data := bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47}, 256)
			w, path := sessionImagePublicationFixture(t, data)
			if setup == "partial" {
				if err := os.WriteFile(path, data[:3], 0600); err != nil {
					t.Fatal(err)
				}
			}
			var group sync.WaitGroup
			errors := make(chan error, 8)
			start := make(chan struct{})
			for i := 0; i < 8; i++ {
				writer := w
				if setup == "fresh-independent-stores" {
					writer = NewWriter(&Store{root: w.store.root}, w.sessionID)
				}
				group.Add(1)
				go func() {
					defer group.Done()
					<-start
					ref, err := writer.ExternalizeImage(context.Background(), "image/png", data)
					if err == nil {
						var got []byte
						got, err = os.ReadFile(ref.Path)
						if err == nil && !bytes.Equal(got, data) {
							err = fmt.Errorf("partial image returned: %d/%d", len(got), len(data))
						}
					}
					errors <- err
				}()
			}
			close(start)
			group.Wait()
			close(errors)
			for err := range errors {
				if err != nil {
					t.Fatal(err)
				}
			}
			assertNoSessionImageTemporaries(t, w)
		})
	}
}

func assertNoSessionImageTemporaries(t *testing.T, w *Writer) {
	t.Helper()
	entries, err := os.ReadDir(w.store.sessionMediaDir(w.sessionID))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".image-") {
			t.Fatalf("own temporary leaked: %s", e.Name())
		}
	}
}

func TestSessionImagePublicationCompleteDedupDoesNotReplace(t *testing.T) {
	data := []byte("keep complete file")
	w, path := sessionImagePublicationFixture(t, data)
	first, err := w.ExternalizeImage(context.Background(), "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	// Keep an open descriptor to ensure the complete-file fast path needs no rename.
	original, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	info, err := original.Stat()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, err := w.ExternalizeImage(context.Background(), "image/png", data)
		if err != nil || got != first {
			t.Fatalf("dedup changed ref: %+v / %v", got, err)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(info, after) {
		t.Fatal("dedup replaced an already complete file")
	}
	assertNoSessionImageTemporaries(t, w)
}

func TestSessionImagePublicationRejectsNonregular(t *testing.T) {
	data := []byte("directory must not count as an image")
	w, path := sessionImagePublicationFixture(t, data)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	ref, err := w.ExternalizeImage(context.Background(), "image/png", data)
	if err == nil || !reflect.DeepEqual(ref, llm.ImageRef{}) {
		t.Fatalf("directory accepted as image: %+v / %v", ref, err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("directory damaged: %v", err)
	}
	assertNoSessionImageTemporaries(t, w)
}

func TestSessionImagePublicationRejectsSymlink(t *testing.T) {
	data := []byte("symlink must not count as an image")
	w, path := sessionImagePublicationFixture(t, data)
	target := filepath.Join(w.store.root, "outside.png")
	if err := os.WriteFile(target, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	ref, err := w.ExternalizeImage(context.Background(), "image/png", data)
	if err == nil || !reflect.DeepEqual(ref, llm.ImageRef{}) {
		t.Fatalf("symlink accepted as image: %+v / %v", ref, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("symlink target changed")
	}
	assertNoSessionImageTemporaries(t, w)
}

func TestSessionImagePublicationFailedReplacePreservesFinal(t *testing.T) {
	data := []byte("good published bytes")
	_, path := sessionImagePublicationFixture(t, data)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceSessionImage(filepath.Join(filepath.Dir(path), "absent-source"), path, true); err == nil {
		t.Fatal("missing source rename succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("failed replace damaged final image")
	}
}

func TestSessionImagePublicationLegacyRepairAndPortableResume(t *testing.T) {
	raw := []byte("legacy original bytes repair incomplete file")
	w, path := sessionImagePublicationFixture(t, raw)
	if err := os.WriteFile(path, raw[:3], 0600); err != nil {
		t.Fatal(err)
	}
	original := llm.ImageRef{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(raw), Name: "keep.png", Active: true}
	msgs := []llm.Message{{Role: llm.RoleUser, Parts: []llm.ContentPart{{Type: llm.PartTypeImage, Image: &original}}}}
	got := w.store.externalizeModelImages(w.sessionID, msgs)
	img := got[0].Parts[0].Image
	if img.Data != "" || img.Path != path || img.Active || img.Name != original.Name || img.ID == "" {
		t.Fatalf("bad converted legacy image: %+v", img)
	}
	saved, err := os.ReadFile(img.Path)
	if err != nil || !bytes.Equal(saved, raw) {
		t.Fatal("legacy repair did not publish exact pixels")
	}
	// Move the portable data tree; file-backed history repairs only its root.
	moved := filepath.Join(t.TempDir(), "portable-data")
	if err := os.Rename(w.store.root, moved); err != nil {
		t.Fatal(err)
	}
	resumed := (&Store{root: moved}).externalizeModelImages(w.sessionID, got)
	repaired := resumed[0].Parts[0].Image
	if repaired.ID != img.ID || repaired.Name != img.Name || repaired.Path == path || repaired.Active {
		t.Fatalf("portable ref changed metadata: %+v", repaired)
	}
	saved, err = os.ReadFile(repaired.Path)
	if err != nil || !bytes.Equal(saved, raw) {
		t.Fatal("portable resume lost exact pixels")
	}
}
