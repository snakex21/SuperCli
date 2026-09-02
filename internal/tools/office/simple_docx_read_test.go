package office

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSimpleDocxMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteSimpleDocx(file, "# Tytuł\n\nTreść dokumentu.\n\n## Sekcja\n\nDalszy tekst."); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	text, err := ReadSimpleDocxMarkdown(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Tytuł", "Treść dokumentu.", "## Sekcja", "Dalszy tekst."} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
}
