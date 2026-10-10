package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadExportGrantIsInvocationAndFolderScoped(t *testing.T) {
	prior := IsUnsandboxed()
	SetUnsandboxed(false)
	t.Cleanup(func() { SetUnsandboxed(prior) })
	root := t.TempDir()
	workspace := filepath.Join(root, "project")
	downloads := filepath.Join(root, "Downloads") // The missing folder is created by the tool.
	ctx := WithDownloadExportDirs(context.Background(), downloads)
	destination := filepath.Join(downloads, "gifs", "funny.gif")
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRoot, "Downloads", "gifs", "funny.gif")
	if full, err := ResolveDownloadDestination(ctx, workspace, destination); err != nil || full != want {
		t.Fatalf("requested missing export folder: full=%q err=%v", full, err)
	}
	// A delegated tool inherits the human's scope through its child context,
	// while remaining unable to select a different export folder.
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	if full, err := ResolveDownloadDestination(child, workspace, destination); err != nil || full != want {
		t.Fatalf("delegated destination lost its human grant: full=%q err=%v", full, err)
	}
	for _, test := range []struct {
		ctx  context.Context
		path string
	}{
		{context.Background(), destination},
		{WithDownloadExportDirs(ctx), destination}, // Fresh turn drops the old grant.
		{ctx, filepath.Join(root, "unrequested", "file.gif")},
		{child, filepath.Join(root, "unrequested", "child-file.gif")},
		{ctx, filepath.Join(downloads, "..", "escape.gif")},
		{ctx, downloads},
		{ctx, filepath.Join("..", "Downloads", "relative.gif")},
	} {
		if full, err := ResolveDownloadDestination(test.ctx, workspace, test.path); !errors.Is(err, ErrEscape) || full != "" {
			t.Errorf("ungranted destination %q: full=%q err=%v", test.path, full, err)
		}
	}
	if _, err := ResolveSafe(workspace, destination); !errors.Is(err, ErrEscape) || IsUnsandboxed() {
		t.Fatalf("download grant opened ordinary file permissions: err=%v allow-all=%v", err, IsUnsandboxed())
	}
}

func TestDownloadExportRejectsSymlinkEscapeAndChangedGrantRoot(t *testing.T) {
	prior := IsUnsandboxed()
	SetUnsandboxed(false)
	t.Cleanup(func() { SetUnsandboxed(prior) })
	root := t.TempDir()
	workspace, downloads, outside := filepath.Join(root, "project"), filepath.Join(root, "Downloads"), filepath.Join(root, "other")
	for _, dir := range []string{downloads, outside} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(downloads, "redirect")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	ctx := WithDownloadExportDirs(context.Background(), downloads)
	if _, err := ResolveDownloadDestination(ctx, workspace, filepath.Join(link, "escape.gif")); !errors.Is(err, ErrEscape) {
		t.Fatalf("symlink descendant escaped export folder: %v", err)
	}
	alias := filepath.Join(root, "DownloadsAlias")
	if err := os.Symlink(downloads, alias); err != nil {
		t.Fatal(err)
	}
	aliasCtx := WithDownloadExportDirs(context.Background(), alias)
	if _, err := ResolveDownloadDestination(aliasCtx, workspace, filepath.Join(alias, "file.gif")); err != nil {
		t.Fatalf("explicitly requested alias failed: %v", err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDownloadDestination(aliasCtx, workspace, filepath.Join(alias, "file.gif")); !errors.Is(err, ErrEscape) {
		t.Fatalf("changed granted root accepted: %v", err)
	}
}

func TestDownloadDestinationHonorsExplicitAllowAll(t *testing.T) {
	prior := IsUnsandboxed()
	SetUnsandboxed(true)
	t.Cleanup(func() { SetUnsandboxed(prior) })
	root := t.TempDir()
	workspace, destination := filepath.Join(root, "project"), filepath.Join(root, "external", "file.gif")
	if full, err := ResolveDownloadDestination(context.Background(), workspace, destination); err != nil || full == "" {
		t.Fatalf("allow-all outside destination rejected: full=%q err=%v", full, err)
	}
	if _, err := ResolveWithin(workspace, destination); !errors.Is(err, ErrEscape) {
		t.Fatalf("download changed strict browser boundary: %v", err)
	}
}
