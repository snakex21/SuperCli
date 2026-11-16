package export

import (
	"os"
	"path/filepath"
	"testing"
)

// Integration test: needs wkhtmltopdf on PATH (installed on the CI image).
func TestExportPDF(t *testing.T) {
	dir := t.TempDir()
	html := filepath.Join(dir, "page.html")
	if err := os.WriteFile(html, []byte("<h1>docgen</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	pdf := filepath.Join(dir, "page.pdf")
	if err := RenderPDF(html, pdf); err != nil {
		t.Fatalf("RenderPDF: %v", err)
	}
	if st, err := os.Stat(pdf); err != nil || st.Size() == 0 {
		t.Fatalf("no PDF written: %v", err)
	}
}
