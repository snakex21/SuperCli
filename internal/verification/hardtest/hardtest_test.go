package hardtest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectGo(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte("module x"), 0644); err != nil {
		t.Fatal(err)
	}
	got := Detect(d)
	if len(got) != 3 {
		t.Fatalf("checks=%d", len(got))
	}
}
