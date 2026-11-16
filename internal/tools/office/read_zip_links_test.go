package office

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"supercli/internal/tools/sandbox"
)

func zipFixtureLink(t *testing.T, portable, link, destination, kind string) {
	t.Helper()
	if !sandbox.IsUnder(portable, link) || !sandbox.IsUnder(portable, destination) {
		t.Fatal("link fixture must remain in its private temporary directory")
	}
	if kind == "junction" {
		if runtime.GOOS != "windows" {
			t.Skip("junction fixture is Windows-only")
		}
		if output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, destination).CombinedOutput(); err != nil {
			t.Skipf("fixture junction unavailable: %v (%s)", err, output)
		}
	} else if err := os.Symlink(destination, link); err != nil {
		t.Skipf("fixture symlink unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
}

func zipOutsideLinkFixture(t *testing.T, kind string, outsideWorkspace bool) (base, external, target string) {
	t.Helper()
	portable := t.TempDir()
	base, external = filepath.Join(portable, "workspace"), filepath.Join(portable, "workspace", "elsewhere")
	if outsideWorkspace {
		external = filepath.Join(portable, "elsewhere")
	}
	target = filepath.Join(base, "out")
	for _, dir := range []string{base, external, target} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(external, "file.txt"), []byte("initial-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	zipFixtureLink(t, portable, filepath.Join(target, "link"), external, kind)
	writeTestZip(t, base, "link.zip", map[string]string{"link/file.txt": "archive-value\n"})
	return base, external, target
}

func zipExtractArgs(path, target string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"path": path, "action": "extract", "target_dir": target})
	return raw
}

// TestReadZipRejectsExistingDirectoryLinksOutsideTarget: an existing directory
// SYMLINK inside the target that resolves outside it is refused.
//
// A directory JUNCTION is different (owner decision, docs/configuration.md
// "Links inside the workspace"): a junction inside the workspace is a
// deliberate grant by whoever created it, so extraction through it is
// allowed and lands in the junction target, inside or outside the workspace.
func TestReadZipRejectsExistingDirectoryLinksOutsideTarget(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		for _, outsideWorkspace := range []bool{false, true} {
			for _, unsandboxed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/outside-workspace=%v/unsandboxed=%v", kind, outsideWorkspace, unsandboxed), func(t *testing.T) {
					prior := sandbox.IsUnsandboxed()
					sandbox.SetUnsandboxed(unsandboxed)
					defer sandbox.SetUnsandboxed(prior)
					base, external, target := zipOutsideLinkFixture(t, kind, outsideWorkspace)
					registry := NewRegistry()
					registry.MustRegister(NewReadZip(base, 0).Spec())
					result, err := registry.Execute(context.Background(), "read_zip", zipExtractArgs("link.zip", target))
					data, readErr := os.ReadFile(filepath.Join(external, "file.txt"))
					if kind == "junction" {
						if err != nil || result.Err != nil {
							t.Fatalf("extraction through a junction grant refused: err=%v resultErr=%v", err, result.Err)
						}
						if readErr != nil || string(data) != "archive-value\n" {
							t.Errorf("extraction did not land in the junction target: readErr=%v", readErr)
						}
						return
					}
					if err == nil && result.Err == nil {
						t.Error("extraction followed an existing symlink outside target")
					}
					if readErr != nil || string(data) != "initial-value\n" {
						t.Errorf("external fixture was overwritten: readErr=%v changed=%v", readErr, string(data) != "initial-value\n")
					}
				})
			}
		}
	}
}

func TestReadZipRejectsFinalFileSymlinkOutsideTarget(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("dangling=%v", missing), func(t *testing.T) {
			base := t.TempDir()
			target, external := filepath.Join(base, "out"), filepath.Join(base, "elsewhere")
			for _, dir := range []string{target, external} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			externalFile := filepath.Join(external, "file.txt")
			if !missing {
				if err := os.WriteFile(externalFile, []byte("initial-value\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			zipFixtureLink(t, base, filepath.Join(target, "file.txt"), externalFile, "symlink")
			writeTestZip(t, base, "file.zip", map[string]string{"file.txt": "archive-value\n"})
			result, err := NewReadZip(base, 0).Execute(context.Background(), zipExtractArgs("file.zip", target))
			if err == nil && result.Err == nil {
				t.Error("existing final file symlink bypassed extraction boundary")
			}
			data, readErr := os.ReadFile(externalFile)
			if missing {
				if !os.IsNotExist(readErr) {
					t.Errorf("dangling link created an external file: err=%v", readErr)
				}
			} else if readErr != nil || string(data) != "initial-value\n" {
				t.Errorf("final link overwrote external fixture: err=%v changed=%v", readErr, string(data) != "initial-value\n")
			}
		})
	}
}

// TestReadZipTargetThroughJunctionIsGranted: a new target directory below a
// junction inside the workspace is created in the junction target (the
// junction is a grant), and the entries land there.
func TestReadZipTargetThroughJunctionIsGranted(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junction fixture is Windows-only")
	}
	base, external, target := zipOutsideLinkFixture(t, "junction", false)
	result, err := NewReadZip(base, 0).Execute(context.Background(), zipExtractArgs("link.zip", filepath.Join(target, "link", "new-target")))
	if err != nil || result.Err != nil {
		t.Fatalf("target below a junction grant refused: err=%v resultErr=%v", err, result.Err)
	}
	data, readErr := os.ReadFile(filepath.Join(external, "new-target", "link", "file.txt"))
	if readErr != nil || string(data) != "archive-value\n" {
		t.Errorf("entry not written in the junction target: err=%v", readErr)
	}
	if data, _ := os.ReadFile(filepath.Join(external, "file.txt")); string(data) != "initial-value\n" {
		t.Error("an unrelated file in the junction target was overwritten")
	}
}

// TestReadZipResolvableAliasesAndJunctions: symlinks that resolve inside the
// boundary and junctions (grants) work at every position: the workspace
// itself, an ancestor of it, the target, and an entry's parent directory.
func TestReadZipResolvableAliasesAndJunctions(t *testing.T) {
	for _, kind := range []string{"symlink", "junction"} {
		for _, position := range []string{"base", "above-base", "target", "entry"} {
			t.Run(kind+"/"+position, func(t *testing.T) {
				portable := t.TempDir()
				base := filepath.Join(portable, "real", "workspace")
				if err := os.MkdirAll(base, 0700); err != nil {
					t.Fatal(err)
				}
				toolBase, target, physicalTarget := base, filepath.Join(base, "out"), filepath.Join(base, "out")
				link, destination := filepath.Join(portable, "alias"), base
				entry, physicalFile := "file.txt", filepath.Join(physicalTarget, "file.txt")
				switch position {
				case "base":
					toolBase, target = link, filepath.Join(link, "out")
				case "above-base":
					destination, toolBase = filepath.Join(portable, "real"), filepath.Join(link, "workspace")
					target = filepath.Join(toolBase, "out")
				case "target":
					link, destination = filepath.Join(base, "out-alias"), physicalTarget
					target = link
					if err := os.Mkdir(destination, 0700); err != nil {
						t.Fatal(err)
					}
				case "entry":
					link, destination = filepath.Join(target, "link"), filepath.Join(target, "real")
					entry, physicalFile = "link/file.txt", filepath.Join(destination, "file.txt")
					if err := os.MkdirAll(destination, 0700); err != nil {
						t.Fatal(err)
					}
				}
				zipFixtureLink(t, portable, link, destination, kind)
				writeTestZip(t, base, "alias.zip", map[string]string{entry: "archive-value\n"})
				result, err := NewReadZip(toolBase, 0).Execute(context.Background(), zipExtractArgs("alias.zip", target))
				if err != nil || result.Err != nil {
					t.Fatalf("contained %s should work: err=%v resultErr=%v", kind, err, result.Err)
				}
				data, err := os.ReadFile(physicalFile)
				if err != nil || string(data) != "archive-value\n" {
					t.Errorf("contained %s changed output bytes: err=%v", kind, err)
				}
			})
		}
	}
}

func TestReadZipLinkGuardPreservesNestedExtractionBytes(t *testing.T) {
	base := t.TempDir()
	writeTestZipRaw(t, base, "nested.zip", []struct {
		Name, Content string
		Method        uint16
	}{
		{Name: "nested/", Method: zip.Store},
		{Name: "flat.txt", Content: "AAA", Method: zip.Store},
		{Name: "nested/new.txt", Content: "BBBB", Method: zip.Deflate},
	})
	target := filepath.Join(base, "out")
	tool := NewReadZip(base, 0)
	result, err := tool.Execute(context.Background(), zipExtractArgs("nested.zip", target))
	if err != nil || result.Err != nil {
		t.Fatalf("normal extraction: err=%v resultErr=%v", err, result.Err)
	}
	resolved, err := resolveSandboxed(base, target)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("Extracted 3 entries (7 bytes) to %s:\n  nested/\n  flat.txt\n  nested/new.txt\n", resolved)
	if result.Text != want {
		t.Error("ordinary output text bytes changed")
	}
	for path, content := range map[string]string{"flat.txt": "AAA", "nested/new.txt": "BBBB"} {
		data, err := os.ReadFile(filepath.Join(target, path))
		if err != nil || string(data) != content {
			t.Errorf("ordinary file bytes changed: path=%s err=%v", path, err)
		}
	}
	second, err := tool.Execute(context.Background(), zipExtractArgs("nested.zip", target))
	if err != nil || second.Err != nil || second.Text != result.Text {
		t.Error("ordinary overwrite result changed")
	}
}
