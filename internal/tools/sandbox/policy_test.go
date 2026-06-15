package sandbox

import (
	"path/filepath"
	"testing"
)

func TestAllowDestructive_EmptyHome(t *testing.T) {
	_, err := AllowDestructive("", OpFileWrite, "x")
	if err == nil {
		t.Error("expected error for empty home")
	}
}

func TestAllowDestructive_EmptyPath(t *testing.T) {
	d, err := AllowDestructive("/home/u", OpFileWrite, "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed {
		t.Error("empty path should not be allowed")
	}
}

func TestAllowDestructive_ReadOnlyShortCircuit(t *testing.T) {
	d, err := AllowDestructive("/home/u", OpFileRead, "/anything")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allowed {
		t.Errorf("read should be allowed, got %+v", d)
	}
}

func TestAllowDestructive_NetworkShortCircuit(t *testing.T) {
	d, _ := AllowDestructive("/home/u", OpNetwork, "/anything")
	if !d.Allowed {
		t.Error("network should be allowed at policy level")
	}
}

func TestAllowDestructive_InsideHome(t *testing.T) {
	home := t.TempDir()
	d, err := AllowDestructive(home, OpFileWrite, "foo.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allowed {
		t.Errorf("inside-home write should be allowed, got %+v", d)
	}
}

func TestAllowDestructive_OutsideHome(t *testing.T) {
	home := t.TempDir()
	// "../escape" is relative; ResolveSafe will catch it.
	_, err := AllowDestructive(home, OpFileWrite, "../escape.txt")
	if err != ErrEscape {
		t.Errorf("expected ErrEscape, got %v", err)
	}
}

func TestAllowDestructive_SensitiveRoot(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("Unix-only sensitive root test")
	}
	home := t.TempDir()
	_, err := AllowDestructive(home, OpFileWrite, "/etc/hosts")
	if err != ErrDenied {
		t.Errorf("expected ErrDenied, got %v", err)
	}
}

func TestAllowDestructive_BashInsideHome(t *testing.T) {
	home := t.TempDir()
	d, err := AllowDestructive(home, OpBash, "scripts/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allowed {
		t.Errorf("inside-home bash should be allowed, got %+v", d)
	}
}
