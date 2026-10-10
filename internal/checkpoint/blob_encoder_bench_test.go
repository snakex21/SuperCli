package checkpoint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkSnapshotNewSmallBlobs64 isolates the blob-writing phase of a new
// capture. Every operation stores 64 different 1 KiB files in an empty objects
// directory, so existing-object deduplication cannot hide encoder allocation.
// Git, repository initialization, fixture writes and object cleanup are outside
// the workload. The central runner sets TMP/TEMP to its portable fixture root;
// everything here stays beneath b.TempDir and never opens a real workspace.
func BenchmarkSnapshotNewSmallBlobs64(b *testing.B) {
	b.StopTimer()
	root := b.TempDir()
	home := filepath.Join(root, "workspace")
	repo := filepath.Join(root, "objects.git")
	objects := filepath.Join(repo, "objects")
	if !within(root, objects) {
		b.Fatal("benchmark objects directory escapes its fixture root")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		b.Fatal(err)
	}
	const count, size = 64, 1024
	files := make([]snapshotFile, 0, count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("asset-%03d.txt", i)
		body := make([]byte, size)
		for j := range body {
			body[j] = byte('a' + (i+j)%26)
		}
		copy(body, fmt.Sprintf("synthetic checkpoint blob %03d\n", i))
		full := filepath.Join(home, name)
		if err := os.WriteFile(full, body, 0o600); err != nil {
			b.Fatal(err)
		}
		info, err := os.Stat(full)
		if err != nil {
			b.Fatal(err)
		}
		files = append(files, snapshotFile{path: name, info: info})
	}
	m := &Manager{home: home, repo: repo}
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(count * size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// The resolved absolute target was checked above and is fixed beneath
		// this benchmark's own root; no user-supplied path reaches RemoveAll.
		if err := os.RemoveAll(objects); err != nil {
			b.Fatal(err)
		}
		if err := os.MkdirAll(objects, 0o700); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		var encoder blobEncoder
		for _, file := range files {
			oid, err := m.storeRawBlobWithEncoder(ctx, file, &encoder)
			if err != nil {
				b.Fatal(err)
			}
			if len(oid) != 40 {
				b.Fatalf("unexpected Git blob OID: %q", oid)
			}
		}
	}
	b.StopTimer()
	b.ReportMetric(count, "blobs/op")
}
