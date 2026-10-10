package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools"
	"supercli/internal/tools/sandbox"
)

func TestDownloadsExportRequiresDirectHumanDestination(t *testing.T) {
	for _, prompt := range []string{
		"cześć pobierz mi do pobranych jakiś fajny gif z tenora",
		"Pobierz GIF z https://tenor.com/view/hello-1 do folderu Pobrane.",
		"Please download an image into my Downloads folder",
		"Can you download a GIF to Downloads?",
		"Ściągnij obrazek do Pobranych",
	} {
		if !requestsDownloadsExport(prompt) {
			t.Errorf("missed direct instruction: %q", prompt)
		}
	}
	for _, prompt := range []string{
		"znajdź gif na Tenorze", "Pobierz gif do assets/", "Jak pobierz do pobranych gif?",
		"Nie pobierz gif do pobranych", "Do not download to Downloads",
		"Model próbował pobierz do pobranych", "przykład: pobierz do pobranych gif",
		"Wyjaśnij polecenie `pobierz do pobranych`", "\"pobierz do pobranych gif\"",
		"Pobierz gif, nie do Pobranych", "Pobierz gif; przykład: zapisz do pobranych",
		"Pobierz gif\nW dokumencie napisano: do pobranych",
		"Pobierz https://example.com/to/downloads do projektu",
	} {
		if requestsDownloadsExport(prompt) {
			t.Errorf("untrusted or unrelated destination granted: %q", prompt)
		}
	}
}

func TestDownloadsExportScopeAndWorkerInheritance(t *testing.T) {
	workspace, downloads, other := t.TempDir(), t.TempDir(), t.TempDir()
	loop := &Loop{baseDir: workspace, userDownloadsDir: func() (string, error) { return downloads, nil }}
	ctx, hint := loop.prepareUserDownloadExport(context.Background(), "Pobierz GIF do Pobranych")
	if !strings.Contains(hint, filepath.ToSlash(downloads)) || !strings.Contains(hint, "Undo/Redo") {
		t.Fatalf("hint=%q", hint)
	}
	if _, err := sandbox.ResolveDownloadDestination(ctx, workspace, filepath.Join(downloads, "a.gif")); err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.ResolveDownloadDestination(ctx, workspace, filepath.Join(other, "a.gif")); err == nil {
		t.Fatal("unrequested export folder granted")
	}
	worker := &Loop{baseDir: workspace}
	child, childHint := worker.prepareUserDownloadExport(ctx, "Pobierz do Pobranych")
	if childHint != "" {
		t.Fatal("worker granted new permission")
	}
	if _, err := sandbox.ResolveDownloadDestination(child, workspace, filepath.Join(downloads, "a.gif")); err != nil {
		t.Fatal("worker lost authorized parent context", err)
	}
	ungiven, _ := worker.prepareUserDownloadExport(context.Background(), "Pobierz do Pobranych")
	if _, err := sandbox.ResolveDownloadDestination(ungiven, workspace, filepath.Join(downloads, "a.gif")); err == nil {
		t.Fatal("worker prompt granted filesystem permission")
	}
	expired, hint := loop.prepareUserDownloadExport(ctx, "cześć")
	if hint != "" {
		t.Fatal("hint leaked")
	}
	if _, err := sandbox.ResolveDownloadDestination(expired, workspace, filepath.Join(downloads, "a.gif")); err == nil {
		t.Fatal("grant survived an unrelated human run")
	}
}

func TestDownloadsExportResolutionFailureDoesNotGuess(t *testing.T) {
	workspace := t.TempDir()
	for _, resolve := range []func() (string, error){
		func() (string, error) { return "", errors.New("missing known folder") },
		func() (string, error) { return "Downloads", nil },
	} {
		l := &Loop{baseDir: workspace, userDownloadsDir: resolve}
		ctx, hint := l.prepareUserDownloadExport(context.Background(), "Pobierz do Pobranych")
		if hint == "" {
			t.Fatal("missing resolution signal")
		}
		if _, err := sandbox.ResolveDownloadDestination(ctx, workspace, filepath.Join(t.TempDir(), "a.gif")); err == nil {
			t.Fatal("invalid callback created grant")
		}
	}
}

func TestDownloadsExportRunContextDoesNotPersistOrAlterPrefix(t *testing.T) {
	workspace, downloads := t.TempDir(), t.TempDir()
	p := &stubProvider{name: "download-export", scripts: [][]llm.Delta{
		{{Content: "Ready.", FinishReason: "stop"}}, {{Content: "Ready.", FinishReason: "stop"}},
	}}
	l, err := NewLoop(LoopConfig{Provider: p, Registry: tools.NewRegistry(), BaseDir: workspace, System: "stable instructions",
		UserDownloadsDir: func() (string, error) { return downloads, nil }})
	if err != nil {
		t.Fatal(err)
	}
	drainEvents(t, mustRun(t, l, "Pobierz GIF do Pobranych"))
	drainEvents(t, mustRun(t, l, "cześć"))
	if p.reqs[0][0].Content != p.reqs[1][0].Content {
		t.Fatal("download context changed stable prefix")
	}
	for i, req := range p.reqs {
		var text strings.Builder
		for _, m := range req {
			text.WriteString(m.Content)
		}
		if strings.Contains(text.String(), filepath.ToSlash(downloads)) != (i == 0) {
			t.Fatalf("transient path in request %d", i)
		}
	}
	for _, m := range l.Messages {
		if strings.Contains(m.Content, filepath.ToSlash(downloads)) {
			t.Fatal("grant hint persisted in history")
		}
	}
}
