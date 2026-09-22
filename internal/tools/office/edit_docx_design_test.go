package office

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditDocx_CreateBotanicalDesignIsCompleteAndLandscapeInOneCall(t *testing.T) {
	dir := t.TempDir()
	args, err := json.Marshal(map[string]any{
		"path": "plan.docx", "action": "create", "design": "botanical",
		"text": "# PLAN ZAJĘĆ\n\n| PONIEDZIAŁEK | WTOREK | ŚRODA | CZWARTEK | PIĄTEK |\n|---|---|---|---|---|\n| Matematyka | Polski | Biologia | Historia | Angielski |\n| WF | Muzyka | Chemia | Plastyka | Informatyka |",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewEditDocx(dir).Execute(t.Context(), args)
	if err != nil || result.Err != nil {
		t.Fatalf("create botanical result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Text, "design=botanical") || !strings.Contains(result.Text, "landscape=true") ||
		!strings.Contains(result.Text, "do not read or format") {
		t.Fatalf("result should terminate the create workflow: %s", result.Text)
	}

	path := filepath.Join(dir, "plan.docx")
	document, err := readZipEntry(path, docxDocumentEntry, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	styles, err := readZipEntry(path, "word/styles.xml", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := readZipEntry(path, "word/settings.xml", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for label, pair := range map[string][2][]byte{
		"page background": {document, []byte(`<w:background w:color="F7FAF4"/>`)},
		"landscape":       {document, []byte(`w:orient="landscape"`)},
		"header fill":     {document, []byte(`w:fill="C9D9C1"`)},
		"stripe fill":     {document, []byte(`w:fill="F1F5EE"`)},
		"strong borders":  {document, []byte(`w:sz="14" w:color="85917F"`)},
		"header height":   {document, []byte(`w:trHeight w:val="720"`)},
		"body height":     {document, []byte(`w:trHeight w:val="650"`)},
		"centered title":  {styles, []byte(`<w:jc w:val="center"/>`)},
		"document font":   {styles, []byte(`w:ascii="Aptos"`)},
		"background mode": {settings, []byte(`<w:displayBackgroundShape/>`)},
	} {
		if !bytes.Contains(pair[0], pair[1]) {
			t.Errorf("%s missing %q", label, pair[1])
		}
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("new document unexpectedly created a backup: %v", err)
	}
}

func TestEditDocx_CreateDerivesPaletteFromOneAccent(t *testing.T) {
	dir := t.TempDir()
	args, err := json.Marshal(map[string]any{
		"path": "accent.docx", "action": "create", "design": "minimal", "accent_color": "#3A7D44",
		"text": "# Tytuł\n| A | B |\n|---|---|\n| 1 | 2 |",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewEditDocx(dir).Execute(t.Context(), args)
	if err != nil || result.Err != nil {
		t.Fatalf("create custom accent result=%+v err=%v", result, err)
	}
	document, _ := readZipEntry(filepath.Join(dir, "accent.docx"), docxDocumentEntry, 1<<20)
	styles, _ := readZipEntry(filepath.Join(dir, "accent.docx"), "word/styles.xml", 1<<20)
	if !bytes.Contains(styles, []byte(`w:color w:val="3A7D44"`)) {
		t.Fatal("custom accent was not applied to document styles")
	}
	if !bytes.Contains(document, []byte(`w:background w:color="F8FAF8"`)) ||
		!bytes.Contains(document, []byte(`w:fill="3A7D44"`)) {
		t.Fatal("accent should produce a coordinated page tint and readable header")
	}
}

func TestEditDocx_CreatePlainRetainsLegacyMinimalPackage(t *testing.T) {
	dir := t.TempDir()
	args, err := json.Marshal(map[string]any{
		"path": "plain.docx", "action": "create", "design": "plain", "text": "# Tytuł\nTekst",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewEditDocx(dir).Execute(t.Context(), args)
	if err != nil || result.Err != nil {
		t.Fatalf("create plain result=%+v err=%v", result, err)
	}
	path := filepath.Join(dir, "plain.docx")
	if _, err := readZipEntry(path, "word/settings.xml", 1<<20); err == nil {
		t.Fatal("plain design should keep the legacy minimal package")
	}
	document, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	if bytes.Contains(document, []byte("<w:background")) {
		t.Fatal("plain design unexpectedly added page decoration")
	}
}

func TestResolveDocxDesignRejectsInvalidCompactOptions(t *testing.T) {
	if _, err := resolveDocxDesign("neon-chaos", "", nil, 0); err == nil {
		t.Fatal("unknown preset should fail")
	}
	if _, err := resolveDocxDesign("botanical", "GG0000", nil, 0); err == nil {
		t.Fatal("invalid accent should fail")
	}
	if _, err := resolveDocxDesign("plain", "112233", nil, 0); err == nil {
		t.Fatal("plain design must reject accent override")
	}
}
