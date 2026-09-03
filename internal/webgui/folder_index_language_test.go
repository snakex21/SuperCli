package webgui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/system/config"
)

// The indexing prompts are written in English on purpose (models follow English
// instructions more reliably), and the *output* language is the one thing that
// varies with the UI setting. The echo provider repeats the prompt verbatim, so
// asserting on the produced note proves the whole chain — config.toml, the
// resolved UI language, the prompt builder — really steers the model.
func TestDocumentIndexPromptRequestsTheUILanguage(t *testing.T) {
	for _, testCase := range []struct {
		language string
		want     string
		reject   string
	}{
		{language: "pl", want: "Respond in Polish.", reject: "Respond in English."},
		{language: "en", want: "Respond in English.", reject: "Respond in Polish."},
	} {
		t.Run(testCase.language, func(t *testing.T) {
			dataDir := t.TempDir()
			home := t.TempDir()
			documents := t.TempDir()
			if err := os.WriteFile(filepath.Join(documents, "note.md"), []byte("# Aurora\nRelease in October."), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := config.SetLanguage(dataDir, home, testCase.language); err != nil {
				t.Fatal(err)
			}
			eng, err := NewEngine(echoConfig(), home, dataDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = eng.Close() })
			server := NewServer(eng, false)

			if got := server.uiLanguage(); got != testCase.language {
				t.Fatalf("uiLanguage() = %q, want %q", got, testCase.language)
			}

			result := scanIndexedFolder(documents)
			cache := folderIndexCache{Files: map[string]folderIndexCacheItem{}}
			server.addDocumentIndexPreviews(context.Background(), &result,
				folderIndexConfig{VisionModel: "echo-test"}, &cache, nil)

			joined := strings.Join(mapValues(result.indexPreview), "\n") + "\n" + result.FolderSummary
			if !strings.Contains(joined, testCase.want) {
				t.Fatalf("indexing prompt did not request %q; got %q", testCase.want, joined)
			}
			if strings.Contains(joined, testCase.reject) {
				t.Fatalf("indexing prompt leaked %q for language %q", testCase.reject, testCase.language)
			}
		})
	}
}

// Prose that a user reads must not be hardcoded in Go. The prompts themselves
// are model-facing English; this guards the one Polish phrase that used to pin
// every summary and image caption to a single language.
func TestIndexingPromptsCarryNoHardcodedOutputLanguage(t *testing.T) {
	for _, name := range []string{"folder_document_summary.go", "folder_index_scan.go"} {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(source), "po polsku") {
			t.Errorf("%s pins the model's output language instead of using respondInLanguage", name)
		}
	}
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, value := range m {
		out = append(out, value)
	}
	return out
}
