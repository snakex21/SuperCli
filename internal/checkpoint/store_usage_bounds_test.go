package checkpoint

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"math"
	"testing"
)

func compressedUsageFixture(t *testing.T, kind string, payload []byte) int64 {
	t.Helper()
	var encoded bytes.Buffer
	w := zlib.NewWriter(&encoded)
	if _, err := fmt.Fprintf(w, "%s %d%c", kind, len(payload), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return int64(encoded.Len())
}

func TestStoreUsageObjectBoundCoversActualCurrentCompressor(t *testing.T) {
	for _, size := range []int{0, 1, 80, 16383, 16384, 32769, 65537} {
		for _, compressible := range []bool{false, true} {
			payload := make([]byte, size)
			x := uint32(0x31415926)
			for i := range payload {
				x ^= x << 13
				x ^= x >> 17
				x ^= x << 5
				if compressible {
					payload[i] = 'a'
				} else {
					payload[i] = byte(x)
				}
			}
			bound, err := StoreUsageObjectBound(int64(size), 1)
			actual := compressedUsageFixture(t, "commit", payload)
			if err != nil || actual > bound {
				t.Fatalf("size=%d compressible=%v actual=%d upper=%d err=%v", size, compressible, actual, bound, err)
			}
		}
	}
	for _, args := range [][2]int64{{-1, 1}, {1, -1}, {1, 0}, {math.MaxInt64, 1}, {0, math.MaxInt64}} {
		if _, err := StoreUsageObjectBound(args[0], args[1]); err == nil {
			t.Fatalf("unsafe bound accepted: %v", args)
		}
	}
}

func TestStoreUsageTreeShapeBoundsSharedDirectoryObjects(t *testing.T) {
	var shape StoreUsageTreeShape
	for _, path := range []string{"a/b.txt", "a/c.txt"} {
		if err := shape.AddPath(path); err != nil {
			t.Fatal(err)
		}
	}
	// Real serialized tree bodies: one shared ancestor, two file entries.
	entry := func(mode, name string) []byte {
		b := []byte(mode + " " + name + "\x00")
		return append(b, make([]byte, 20)...)
	}
	root := entry("40000", "a")
	child := append(entry("100644", "b.txt"), entry("100755", "c.txt")...)
	commit := []byte("tree 0000000000000000000000000000000000000000\nauthor synthetic <test@local> 0 +0000\ncommitter synthetic <test@local> 0 +0000\n\nsynthetic\n")
	actual := compressedUsageFixture(t, "tree", root) + compressedUsageFixture(t, "tree", child) + compressedUsageFixture(t, "commit", commit)
	upper, err := shape.Bound(int64(len(commit)))
	if err != nil || upper < actual {
		t.Fatalf("shared ancestor undercounted: actual=%d upper=%d err=%v", actual, upper, err)
	}
	if _, err := StoreUsageRefBound(-1); err == nil {
		t.Fatal("negative ref writes accepted")
	}
	if upper, err := StoreUsageRefBound(3); err != nil || upper != 123 {
		t.Fatalf("loose ref bytes: %d %v", upper, err)
	}
	if err := shape.AddPath(".git/config"); err == nil {
		t.Fatal("unsafe tree path accepted")
	}
}
