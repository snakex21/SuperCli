package checkpoint

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newBlobEncoderTestManager(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	m := &Manager{home: filepath.Join(root, "workspace"), repo: filepath.Join(root, "objects.git")}
	for _, directory := range []string{m.home, filepath.Join(m.repo, "objects")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func writeBlobEncoderFixture(t *testing.T, m *Manager, name string, body []byte) snapshotFile {
	t.Helper()
	full := filepath.Join(m.home, name)
	if err := os.WriteFile(full, body, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	return snapshotFile{path: name, info: info}
}

func expectedGitBlob(body []byte) ([]byte, string) {
	data := append([]byte(fmt.Sprintf("blob %d\x00", len(body))), body...)
	digest := sha1.Sum(data)
	return data, hex.EncodeToString(digest[:])
}

func assertEncodedGitBlob(t *testing.T, m *Manager, oid string, body []byte) {
	t.Helper()
	want, wantOID := expectedGitBlob(body)
	if oid != wantOID {
		t.Fatalf("Git blob OID = %s, want %s", oid, wantOID)
	}
	input, err := os.Open(filepath.Join(m.repo, "objects", oid[:2], oid[2:]))
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	reader, err := zlib.NewReader(input)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(io.LimitReader(reader, int64(len(want)+1)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Git blob roundtrip differs: got %d bytes, want %d", len(got), len(want))
	}
}

func assertBlobEncoderObjects(t *testing.T, m *Manager, oids ...string) {
	t.Helper()
	objects := filepath.Join(m.repo, "objects")
	want := make(map[string]bool, len(oids))
	for _, oid := range oids {
		want[oid[:2]+"/"+oid[2:]] = true
	}
	if err := filepath.WalkDir(objects, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(objects, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		if !want[key] {
			t.Errorf("unexpected object or leaked temporary file: %s", key)
		}
		delete(want, key)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for key := range want {
		t.Errorf("missing expected object: %s", key)
	}
}

func TestBlobEncoderPreservesGitObjectsAcrossReset(t *testing.T) {
	m := newBlobEncoderTestManager(t)
	var encoder blobEncoder
	fixtures := [][]byte{
		{},
		[]byte("hello world\n"),
		[]byte("CRLF\r\n\x00\xff\x01\r\n"),
		bytes.Repeat([]byte("large\x00binary\xff"), 6000),
		[]byte("small blob after a large one"),
	}
	var oids []string
	for i, body := range fixtures {
		file := writeBlobEncoderFixture(t, m, fmt.Sprintf("fixture-%d.bin", i), body)
		oid, err := m.storeRawBlobWithEncoder(context.Background(), file, &encoder)
		if err != nil {
			t.Fatal(err)
		}
		assertEncodedGitBlob(t, m, oid, body)
		oids = append(oids, oid)
		// The compatibility entry point must return the identical raw Git OID.
		legacyOID, err := m.storeRawBlob(context.Background(), file)
		if err != nil || legacyOID != oid {
			t.Fatalf("compatibility wrapper: oid=%s err=%v", legacyOID, err)
		}
	}
	if oids[0] != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" || oids[1] != "3b18e512dba79e4c8300dd08aeb37f8e728b8dad" {
		t.Fatalf("known Git blob OIDs changed: %v", oids[:2])
	}
	assertBlobEncoderObjects(t, m, oids...)
}

func TestBlobEncoderDeduplicatesBeforeAllocatingCompressor(t *testing.T) {
	m := newBlobEncoderTestManager(t)
	body := []byte("existing byte-for-byte object\r\n")
	file := writeBlobEncoderFixture(t, m, "existing.bin", body)
	oid, err := m.storeRawBlob(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	object := filepath.Join(m.repo, "objects", oid[:2], oid[2:])
	stamp := time.Unix(1_600_000_000, 0)
	if err := os.Chtimes(object, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(object)
	if err != nil {
		t.Fatal(err)
	}
	var encoder blobEncoder
	got, err := m.storeRawBlobWithEncoder(context.Background(), file, &encoder)
	if err != nil || got != oid {
		t.Fatalf("deduplicated oid=%s err=%v", got, err)
	}
	if encoder.compressed != nil {
		t.Fatal("existing object allocated compression state")
	}
	after, err := os.Stat(object)
	if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("deduplication replaced the existing object: %v", err)
	}
	assertEncodedGitBlob(t, m, oid, body)
	assertBlobEncoderObjects(t, m, oid)
}

// contextReader checks Err before each file read. These fixtures fit into one
// shared copy buffer, so the second call happens after the first SHA1 pass and
// temporary output creation, immediately before the compression pass reads.
// No concurrent writer or timing assumption is needed to exercise that race.
type blobReadCallbackContext struct {
	context.Context
	calls  int
	onRead func(int)
}

func (c *blobReadCallbackContext) Err() error {
	c.calls++
	c.onRead(c.calls)
	return c.Context.Err()
}

func TestBlobEncoderRejectsChangedInputAfterSuccessfulBlob(t *testing.T) {
	for _, change := range []string{"growth", "same-size-rewrite"} {
		t.Run(change, func(t *testing.T) {
			m := newBlobEncoderTestManager(t)
			var encoder blobEncoder
			stableBody := []byte("previous successful blob must remain readable")
			stable := writeBlobEncoderFixture(t, m, "stable.bin", stableBody)
			stableOID, err := m.storeRawBlobWithEncoder(context.Background(), stable, &encoder)
			if err != nil {
				t.Fatal(err)
			}
			original := bytes.Repeat([]byte("a"), 2048)
			changed := bytes.Repeat([]byte("b"), len(original))
			if change == "growth" {
				changed = append(append([]byte(nil), original...), []byte("appended bytes")...)
			}
			file := writeBlobEncoderFixture(t, m, "changing.bin", original)
			full := filepath.Join(m.home, file.path)
			mutated := false
			ctx := &blobReadCallbackContext{Context: context.Background()}
			ctx.onRead = func(call int) {
				if call != 2 {
					return
				}
				if err := os.WriteFile(full, changed, 0o600); err != nil {
					t.Fatal(err)
				}
				// Keep mtime identical, so same-size changes require the second
				// SHA1 check and growth still requires the bounded length check.
				if err := os.Chtimes(full, file.info.ModTime(), file.info.ModTime()); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(full)
				if err != nil || !info.ModTime().Equal(file.info.ModTime()) {
					t.Fatalf("could not preserve fixture mtime: %v", err)
				}
				mutated = true
			}
			oid, err := m.storeRawBlobWithEncoder(ctx, file, &encoder)
			if !mutated || oid != "" || err == nil || !strings.Contains(err.Error(), "source changed") {
				t.Fatalf("changed input accepted: mutated=%v oid=%s err=%v", mutated, oid, err)
			}
			assertBlobEncoderObjects(t, m, stableOID)
			assertEncodedGitBlob(t, m, stableOID, stableBody)
			// Reset must also discard an aborted compression stream. A new
			// metadata snapshot can safely capture the now-stable changed file.
			info, err := os.Stat(full)
			if err != nil {
				t.Fatal(err)
			}
			file.info = info
			recoveredOID, err := m.storeRawBlobWithEncoder(context.Background(), file, &encoder)
			if err != nil {
				t.Fatal(err)
			}
			assertEncodedGitBlob(t, m, recoveredOID, changed)
			assertBlobEncoderObjects(t, m, stableOID, recoveredOID)
		})
	}
}
