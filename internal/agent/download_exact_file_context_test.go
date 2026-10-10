package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools/sandbox"
)

func TestDownloadExactFileTargetsAuthorizeOnlyHumanFilenameAndRetainBoundedFollowup(t *testing.T) {
	workspace, downloads := t.TempDir(), t.TempDir()
	file := filepath.Join(t.TempDir(), "not-yet-created", "chosen.gif")
	for _, prompt := range []string{
		"Download https://assets.example.test/a.gif to `" + file + "`",
		"Możesz pobrać GIF z internetu i zapisać go jako `" + file + "`?",
		"Download https://assets.example.test/a.gif as `" + file + "`",
	} {
		t.Run(prompt, func(t *testing.T) {
			loop := &Loop{baseDir: workspace, userDownloadsDir: func() (string, error) { return downloads, nil }}
			ctx, hint := loop.prepareUserDownloadExport(context.Background(), prompt)
			if !strings.Contains(hint, "exact filenames") || !strings.Contains(hint, filepath.ToSlash(file)) {
				t.Fatalf("missing exact filename contract: %q", hint)
			}
			if _, err := sandbox.ResolveDownloadDestination(ctx, workspace, file); err != nil {
				t.Fatal("explicit file rejected", err)
			}
			loop.rememberUserDownloadRequest(prompt)
			followup, _ := loop.prepareUserDownloadExport(context.Background(), "Spróbuj pobrać GIF ponownie")
			if _, err := sandbox.ResolveDownloadDestination(followup, workspace, file); err != nil {
				t.Fatal("same exact retry target lost", err)
			}
			for _, denied := range []string{filepath.Dir(file), filepath.Join(filepath.Dir(file), "other.gif"), filepath.Join(file, "child.gif")} {
				for _, scope := range []context.Context{ctx, followup} {
					if _, err := sandbox.ResolveDownloadDestination(scope, workspace, denied); err == nil {
						t.Fatalf("exact target widened into %q", denied)
					}
				}
			}
			worker := &Loop{baseDir: workspace}
			child, _ := worker.prepareUserDownloadExport(ctx, "Pobierz GIF do `"+filepath.Dir(file)+"`")
			if _, err := sandbox.ResolveDownloadDestination(child, workspace, filepath.Join(filepath.Dir(file), "worker.gif")); err == nil {
				t.Fatal("worker widened exact parent scope")
			}
			fresh, _ := loop.prepareUserDownloadExport(ctx, "cześć")
			if _, err := sandbox.ResolveDownloadDestination(fresh, workspace, file); err == nil {
				t.Fatal("unrelated turn retained exact target")
			}
		})
	}
	writer := &rawDownloadHistoryWriter{history: []llm.Message{{Role: llm.RoleUser, Content: "Download a GIF online to `" + file + "`"}}}
	fresh := &Loop{baseDir: workspace, writer: writer, userDownloadsDir: func() (string, error) { return downloads, nil }}
	ctx, _ := fresh.prepareUserDownloadExport(context.Background(), "Can you download it again?")
	if _, err := sandbox.ResolveDownloadDestination(ctx, workspace, file); err != nil {
		t.Fatal("fresh GUI loop lost raw exact-file grant", err)
	}
	if _, err := sandbox.ResolveDownloadDestination(ctx, workspace, filepath.Join(filepath.Dir(file), "other.gif")); err == nil {
		t.Fatal("fresh loop widened exact-file grant")
	}
}

func TestDownloadFolderLabelsAndExistingDirsOverrideAssetSuffix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "collection.gif")
	for _, prompt := range []string{
		"Download two GIFs into folder `" + dir + "`",
		"Pobierz GIF do katalogu `" + dir + "`",
		"Download an image into `" + dir + string(filepath.Separator) + "`",
	} {
		dirs, files := downloadTargetsFromPrompt(prompt)
		if len(dirs) != 1 || len(files) != 0 || !strings.EqualFold(dirs[0], dir) {
			t.Fatalf("explicit folder became exact file: dirs=%v files=%v prompt=%q", dirs, files, prompt)
		}
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	dirs, files := downloadTargetsFromPrompt("Download an image into `" + dir + "`")
	if len(dirs) != 1 || len(files) != 0 {
		t.Fatalf("existing directory became a file: dirs=%v files=%v", dirs, files)
	}
	file := filepath.Join(t.TempDir(), "one.gif")
	dirs, files = downloadTargetsFromPrompt("Download from folder `" + dir + "` to `" + file + "`")
	if len(dirs) != 0 || len(files) != 1 || !strings.EqualFold(files[0], file) {
		t.Fatalf("source folder changed output filename kind: dirs=%v files=%v", dirs, files)
	}
}
