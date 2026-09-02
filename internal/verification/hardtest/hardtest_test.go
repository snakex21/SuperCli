package hardtest

import (
	"os"
	"path/filepath"
	"strings"
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

func TestDetectSuperCLIAddsProtocolInvariants(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte("module supercli\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := Detect(d)
	if len(got) != 4 {
		t.Fatalf("checks=%d, want protocol + standard Go checks", len(got))
	}
	args := strings.Join(got[0].args, " ")
	if got[0].name != "SuperCLI protocol invariants" || !strings.Contains(args, "TestHardProtocol") || !strings.Contains(args, "./internal/agent") {
		t.Fatalf("first check=%+v", got[0])
	}
}
