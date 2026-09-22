package office

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEditDocx_InsertImageEmbedsPortableMediaWithAltTextAndCaption(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "image.docx", []string{"Przed obrazem.", "Po obrazie."})
	imagePath := filepath.Join(dir, "żółty-obraz.png")
	picture := image.NewRGBA(image.Rect(0, 0, 320, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 320; x++ {
			picture.Set(x, y, color.RGBA{R: 244, G: uint8(120 + y/4), B: 40, A: 255})
		}
	}
	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, picture); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	originalImage, _ := os.ReadFile(imagePath)
	args, _ := json.Marshal(map[string]any{
		"path": "image.docx", "action": "insert_image", "image_path": "żółty-obraz.png",
		"after": "/body/p[1]", "width_cm": 8.0, "alt_text": "Żółty prostokąt", "text": "Rysunek 1 Łódź i źdźbło",
	})
	res, err := NewEditDocx(dir).Execute(t.Context(), args)
	if err != nil || res.Err != nil {
		t.Fatalf("insert image err=%v result=%v", err, res.Err)
	}
	embedded, err := readZipEntry(filepath.Join(dir, "image.docx"), "word/media/image1.png", 1<<20)
	if err != nil {
		t.Fatalf("embedded image missing: %v", err)
	}
	if !bytes.Equal(embedded, originalImage) {
		t.Fatal("embedded image bytes changed")
	}
	doc, _ := readZipEntry(filepath.Join(dir, "image.docx"), docxDocumentEntry, 1<<20)
	rels, _ := readZipEntry(filepath.Join(dir, "image.docx"), docxRelsEntry, 1<<20)
	types, _ := readZipEntry(filepath.Join(dir, "image.docx"), docxTypesEntry, 1<<20)
	for label, pair := range map[string][2][]byte{
		"drawing":      {doc, []byte(`<wp:docPr id="1" name="żółty-obraz.png" descr="Żółty prostokąt"/>`)},
		"relation":     {rels, []byte(`Type="` + docxImageRelationship + `" Target="media/image1.png"`)},
		"content type": {types, []byte(`Extension="png" ContentType="image/png"`)},
	} {
		if !bytes.Contains(pair[0], pair[1]) {
			t.Fatalf("%s missing %q", label, pair[1])
		}
	}
	plain := readDocxText(t, dir, "image.docx")
	if !(strings.Index(plain, "Przed obrazem.") < strings.Index(plain, "Rysunek 1 Łódź i źdźbło") &&
		strings.Index(plain, "Rysunek 1 Łódź i źdźbło") < strings.Index(plain, "Po obrazie.")) {
		t.Fatalf("image caption placement is wrong: %s", plain)
	}
	selectors, err := NewReadDocx(dir, 0).Execute(t.Context(), json.RawMessage(`{"path":"image.docx","selectors":true,"formatting":true}`))
	if err != nil || selectors.Err != nil || !strings.Contains(selectors.Text, `image rel=rId1 width=8.00cm alt="Żółty prostokąt"`) {
		t.Fatalf("image selector metadata missing: err=%v result=%v", err, selectors)
	}
}

func TestEditDocx_CreatePreservesUnicodeAndBuildsNativeMarkdownTable(t *testing.T) {
	dir := t.TempDir()
	input := "# Zażółć gęślą jaźń\nPchnąć w tę łódź jeża lub ośm skrzyń fig.\n| Miasto | Opis |\n| --- | --- |\n| Łódź | śnieg, źdźbło i żółć |\n| Český Krumlov | déjà vu i 東京 |"
	args, _ := json.Marshal(map[string]any{"path": "unicode.docx", "action": "create", "text": input})
	res, err := NewEditDocx(dir).Execute(t.Context(), args)
	if err != nil || res.Err != nil {
		t.Fatalf("create Unicode table err=%v result=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "1 table(s)") {
		t.Fatalf("result does not report table creation: %s", res.Text)
	}
	plain := readDocxText(t, dir, "unicode.docx")
	for _, want := range []string{
		"Zażółć gęślą jaźń",
		"Pchnąć w tę łódź jeża lub ośm skrzyń fig.",
		"Miasto | Opis",
		"Łódź | śnieg, źdźbło i żółć",
		"Český Krumlov | déjà vu i 東京",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("Unicode/table text missing %q from %q", want, plain)
		}
	}
	doc, err := readZipEntry(dir+"/unicode.docx", docxDocumentEntry, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{[]byte("<w:tbl>"), []byte("<w:tblHeader/>"), []byte(`w:fill="1F4E78"`), []byte("Zażółć"), []byte("東京")} {
		if !bytes.Contains(doc, want) {
			t.Fatalf("document XML missing %q", want)
		}
	}
}

func TestEditDocx_ReplaceUnicodeAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "replace-unicode.docx", []string{"Zażółć gęślą jaźń."})
	doc, err := readZipEntry(path, docxDocumentEntry, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	doc = bytes.Replace(doc, []byte("<w:t>Zażółć gęślą jaźń.</w:t>"), []byte("<w:t>Zażółć gę</w:t></w:r><w:r><w:t>ślą jaźń.</w:t>"), 1)
	if _, err := editZipEntryInPlace(path, docxDocumentEntry, doc); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")
	args, _ := json.Marshal(map[string]any{
		"path": "replace-unicode.docx", "action": "replace",
		"find": "gęślą jaźń", "replace": "łódź i źdźbło",
	})
	res, err := NewEditDocx(dir).Execute(t.Context(), args)
	if err != nil || res.Err != nil {
		t.Fatalf("Unicode replace err=%v result=%v", err, res.Err)
	}
	if got := readDocxText(t, dir, "replace-unicode.docx"); !strings.Contains(got, "Zażółć łódź i źdźbło.") {
		t.Fatalf("Unicode replacement was corrupted: %q", got)
	}
}

func TestEditDocx_ReplaceManyIsAtomicAndUnicodeSafe(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "many.docx", []string{"Łódź ma żółtą łódź.", "Gdańsk ma źdźbło."})
	tool := NewEditDocx(dir)
	badArgs, _ := json.Marshal(map[string]any{
		"path": "many.docx", "action": "replace_many",
		"replacements": []map[string]any{
			{"find": "żółtą", "replace": "czerwoną", "expected_count": 1},
			{"find": "brak", "replace": "x", "expected_count": 1},
		},
	})
	if _, err := tool.Execute(t.Context(), badArgs); err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("expected atomic count failure, got %v", err)
	}
	if got := readDocxText(t, dir, "many.docx"); !strings.Contains(got, "żółtą") || strings.Contains(got, "czerwoną") {
		t.Fatalf("failed batch leaked a partial edit: %s", got)
	}
	goodArgs, _ := json.Marshal(map[string]any{
		"path": "many.docx", "action": "replace_many",
		"replacements": []map[string]any{
			{"find": "żółtą", "replace": "czerwoną", "expected_count": 1},
			{"find": "źdźbło", "replace": "gęśl", "expected_count": 1},
		},
	})
	res, err := tool.Execute(t.Context(), goodArgs)
	if err != nil || res.Err != nil {
		t.Fatalf("replace_many err=%v result=%v", err, res.Err)
	}
	if got := readDocxText(t, dir, "many.docx"); !strings.Contains(got, "czerwoną łódź") || !strings.Contains(got, "Gdańsk ma gęśl") {
		t.Fatalf("batch replacement failed: %s", got)
	}
}

func TestEditDocx_InsertTableAfterParagraphAndAddUnicodeRow(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "table.docx", []string{"Przed tabelą.", "Po tabeli."})
	tool := NewEditDocx(dir)
	insertArgs, _ := json.Marshal(map[string]any{
		"path": "table.docx", "action": "insert_table", "after": "/body/p[1]",
		"text": "Nazwa\tWartość\nŁódź\tżółć",
	})
	res, err := tool.Execute(t.Context(), insertArgs)
	if err != nil || res.Err != nil {
		t.Fatalf("insert table err=%v result=%v", err, res.Err)
	}
	plain := readDocxText(t, dir, "table.docx")
	if !(strings.Index(plain, "Przed tabelą.") < strings.Index(plain, "Nazwa | Wartość") &&
		strings.Index(plain, "Nazwa | Wartość") < strings.Index(plain, "Po tabeli.")) {
		t.Fatalf("table placement is wrong: %s", plain)
	}

	rowArgs, _ := json.Marshal(map[string]any{
		"path": "table.docx", "action": "table_add_row", "source": "/body/tbl[1]",
		"text": "Gdańsk | źdźbło",
	})
	res, err = tool.Execute(t.Context(), rowArgs)
	if err != nil || res.Err != nil {
		t.Fatalf("add row err=%v result=%v", err, res.Err)
	}
	selectors, err := NewReadDocx(dir, 0).Execute(t.Context(), json.RawMessage(`{"path":"table.docx","selectors":true}`))
	if err != nil || selectors.Err != nil {
		t.Fatalf("read selectors err=%v result=%v", err, selectors.Err)
	}
	for _, want := range []string{
		`/body/tbl[1] [table rows=3 columns=2]`,
		`/body/tbl[1]/tr[3] [row cells=2]`,
		`/body/tbl[1]/tr[3]/tc[1]/p[1] [style=Normal] Gdańsk`,
		`/body/tbl[1]/tr[3]/tc[2]/p[1] [style=Normal] źdźbło`,
	} {
		if !strings.Contains(selectors.Text, want) {
			t.Fatalf("added row selector missing %q: %s", want, selectors.Text)
		}
	}
}

func TestEditDocx_TableAddRowsBatchesSeveralRows(t *testing.T) {
	dir := t.TempDir()
	createArgs, _ := json.Marshal(map[string]any{
		"path": "rows.docx", "action": "create",
		"text": "| Nazwa | Wartość |\n| --- | --- |\n| Start | 1 |",
	})
	tool := NewEditDocx(dir)
	if res, err := tool.Execute(t.Context(), createArgs); err != nil || res.Err != nil {
		t.Fatalf("create err=%v result=%v", err, res.Err)
	}
	rowArgs, _ := json.Marshal(map[string]any{
		"path": "rows.docx", "action": "table_add_rows", "source": "/body/tbl[1]",
		"text": "Łódź | 2\nGdańsk | 3\nŻory | 4",
	})
	res, err := tool.Execute(t.Context(), rowArgs)
	if err != nil || res.Err != nil {
		t.Fatalf("table_add_rows err=%v result=%v", err, res.Err)
	}
	if !strings.Contains(res.Text, "Added 3 row(s)") {
		t.Fatalf("unexpected batch result: %s", res.Text)
	}
	plain := readDocxText(t, dir, "rows.docx")
	for _, want := range []string{"Łódź | 2", "Gdańsk | 3", "Żory | 4"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("batch row missing %q: %s", want, plain)
		}
	}
}

func TestEditDocx_InsertAfterSupportsMixedParagraphsAndTables(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "mixed.docx", []string{"Start.", "Koniec."})
	text := "## Śródtytuł\nTreść z ąęśćźż.\n| A | B |\n| --- | --- |\n| Ą | Ź |"
	args, _ := json.Marshal(map[string]any{
		"path": "mixed.docx", "action": "insert_after", "after": "/body/p[1]", "text": text,
	})
	res, err := NewEditDocx(dir).Execute(t.Context(), args)
	if err != nil || res.Err != nil {
		t.Fatalf("insert mixed content err=%v result=%v", err, res.Err)
	}
	plain := readDocxText(t, dir, "mixed.docx")
	for _, want := range []string{"Start.", "Śródtytuł", "Treść z ąęśćźż.", "A | B", "Ą | Ź", "Koniec."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("inserted content missing %q from %q", want, plain)
		}
	}
}

func TestEditDocx_CreateSupportsListsAndPageBreaks(t *testing.T) {
	dir := t.TempDir()
	input := "# Lista\n- Zażółć gęślą\n- [ ] Sprawdzić Łódź\n- [x] Zachować źdźbło\n1. Pierwszy krok\n2) Drugi krok\nKoniec."
	createArgs, _ := json.Marshal(map[string]any{"path": "lists.docx", "action": "create", "text": input})
	tool := NewEditDocx(dir)
	if res, err := tool.Execute(t.Context(), createArgs); err != nil || res.Err != nil {
		t.Fatalf("create lists err=%v result=%v", err, res.Err)
	}
	plain := readDocxText(t, dir, "lists.docx")
	for _, want := range []string{"•\tZażółć gęślą", "☐\tSprawdzić Łódź", "☒\tZachować źdźbło", "1.\tPierwszy krok", "2)\tDrugi krok"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("list item missing %q from %q", want, plain)
		}
	}
	breakArgs, _ := json.Marshal(map[string]any{
		"path": "lists.docx", "action": "insert_page_break", "after": "/body/p[6]",
	})
	if res, err := tool.Execute(t.Context(), breakArgs); err != nil || res.Err != nil {
		t.Fatalf("insert page break err=%v result=%v", err, res.Err)
	}
	doc, _ := readZipEntry(filepath.Join(dir, "lists.docx"), docxDocumentEntry, 1<<20)
	if bytes.Count(doc, []byte(`<w:br w:type="page"/>`)) != 1 {
		t.Fatalf("native page break missing: %s", doc)
	}
}

func TestEditDocx_FormatAtPreservesTextAndWorksInsideTableCell(t *testing.T) {
	dir := t.TempDir()
	input := "Treść zwykła.\n| Nazwa | Opis |\n| --- | --- |\n| Łódź | źdźbło |"
	createArgs, _ := json.Marshal(map[string]any{"path": "format.docx", "action": "create", "text": input})
	tool := NewEditDocx(dir)
	if res, err := tool.Execute(t.Context(), createArgs); err != nil || res.Err != nil {
		t.Fatalf("create err=%v result=%v", err, res.Err)
	}
	formatArgs, _ := json.Marshal(map[string]any{
		"path": "format.docx", "action": "format_at", "source": "/body/tbl[1]/tr[2]/tc[2]/p[1]",
		"bold": true, "italic": true, "underline": true, "font_size_pt": 13.5,
		"color": "#1f4e78", "alignment": "right",
	})
	if res, err := tool.Execute(t.Context(), formatArgs); err != nil || res.Err != nil {
		t.Fatalf("format cell err=%v result=%v", err, res.Err)
	}
	if got := readDocxText(t, dir, "format.docx"); !strings.Contains(got, "Łódź | źdźbło") {
		t.Fatalf("formatting changed text: %s", got)
	}
	doc, _ := readZipEntry(filepath.Join(dir, "format.docx"), docxDocumentEntry, 1<<20)
	location, err := findDocxParagraphLocation(doc, "/body/tbl[1]/tr[2]/tc[2]/p[1]")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{
		[]byte(`<w:jc w:val="right"/>`), []byte(`<w:b/>`), []byte(`<w:i/>`),
		[]byte(`<w:u w:val="single"/>`), []byte(`<w:sz w:val="27"/>`), []byte(`<w:color w:val="1F4E78"/>`),
	} {
		if !bytes.Contains(location.frag, want) {
			t.Fatalf("formatted cell missing %q: %s", want, location.frag)
		}
	}
}

func TestEditDocx_InsertLinkCreatesEditableExternalHyperlink(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "link.docx", []string{"Początek.", "Koniec."})
	args, _ := json.Marshal(map[string]any{
		"path": "link.docx", "action": "insert_link", "after": "/body/p[1]",
		"text": "Dokumentacja Łódź", "url": "https://example.com/a?x=1&język=pl",
	})
	res, err := NewEditDocx(dir).Execute(t.Context(), args)
	if err != nil || res.Err != nil {
		t.Fatalf("insert link err=%v result=%v", err, res.Err)
	}
	doc, _ := readZipEntry(filepath.Join(dir, "link.docx"), docxDocumentEntry, 1<<20)
	rels, _ := readZipEntry(filepath.Join(dir, "link.docx"), docxRelsEntry, 1<<20)
	for label, pair := range map[string][2][]byte{
		"hyperlink": {doc, []byte(`<w:hyperlink r:id="rId1" w:history="1">`)},
		"label":     {doc, []byte("Dokumentacja Łódź")},
		"target":    {rels, []byte(`Target="https://example.com/a?x=1&amp;język=pl" TargetMode="External"`)},
	} {
		if !bytes.Contains(pair[0], pair[1]) {
			t.Fatalf("%s missing %q", label, pair[1])
		}
	}
	plain := readDocxText(t, dir, "link.docx")
	if !(strings.Index(plain, "Początek.") < strings.Index(plain, "Dokumentacja Łódź") &&
		strings.Index(plain, "Dokumentacja Łódź") < strings.Index(plain, "Koniec.")) {
		t.Fatalf("link placement is wrong: %s", plain)
	}
	selectors, err := NewReadDocx(dir, 0).Execute(t.Context(), json.RawMessage(`{"path":"link.docx","selectors":true,"formatting":true}`))
	if err != nil || selectors.Err != nil || !strings.Contains(selectors.Text, "link rel=rId1") {
		t.Fatalf("link selector metadata missing: err=%v result=%v", err, selectors)
	}
}

func TestEditDocx_DeleteParagraphRowAndTable(t *testing.T) {
	dir := t.TempDir()
	input := "Pierwszy.\nDrugi.\n| A | B |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |"
	createArgs, _ := json.Marshal(map[string]any{"path": "delete.docx", "action": "create", "text": input})
	if res, err := NewEditDocx(dir).Execute(t.Context(), createArgs); err != nil || res.Err != nil {
		t.Fatalf("create err=%v result=%v", err, res.Err)
	}
	tool := NewEditDocx(dir)
	for _, selector := range []string{"/body/p[2]", "/body/tbl[1]/tr[2]"} {
		args, _ := json.Marshal(map[string]any{"path": "delete.docx", "action": "delete_at", "source": selector})
		if res, err := tool.Execute(t.Context(), args); err != nil || res.Err != nil {
			t.Fatalf("delete %s err=%v result=%v", selector, err, res.Err)
		}
	}
	plain := readDocxText(t, dir, "delete.docx")
	if strings.Contains(plain, "Drugi.") || strings.Contains(plain, "1 | 2") {
		t.Fatalf("selected content was not deleted: %s", plain)
	}
	args, _ := json.Marshal(map[string]any{"path": "delete.docx", "action": "delete_at", "source": "/body/tbl[1]"})
	if res, err := tool.Execute(t.Context(), args); err != nil || res.Err != nil {
		t.Fatalf("delete table err=%v result=%v", err, res.Err)
	}
	plain = readDocxText(t, dir, "delete.docx")
	if strings.Contains(plain, "A | B") || !strings.Contains(plain, "Pierwszy.") {
		t.Fatalf("table deletion changed wrong content: %s", plain)
	}
	if _, err := os.Stat(dir + "/delete.docx.bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestParseDocxTableTextSupportsEscapedPipesAndRejectsJaggedRows(t *testing.T) {
	rows, err := parseDocxTableText("Klucz | Opis\nA | lewo \\| prawo")
	if err != nil {
		t.Fatal(err)
	}
	if got := rows[1][1]; got != "lewo | prawo" {
		t.Fatalf("escaped pipe = %q", got)
	}
	if _, err := parseDocxTableText("A | B\n1 | 2 | 3"); err == nil || !strings.Contains(err.Error(), "expected 2") {
		t.Fatalf("jagged table should fail clearly, got %v", err)
	}
}

func TestEditDocx_BatchAppliesMixedUnicodeEditsWithOneBackup(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "batch.docx", []string{"Zażółć gęślą jaźń.", "Tabela poniżej."})
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{
		"path":"batch.docx","action":"batch","operations":[
			{"action":"replace_at","source":"/body/p[1]","text":"Pchnąć w tę łódź jeża lub ośm skrzyń fig."},
			{"action":"format_at","source":"/body/p[1]","bold":true,"color":"1F4E78"},
			{"action":"insert_table","after":"end","text":"Język | Wynik\nPolski | ąćęłńóśźż\nČesky | Příliš žluťoučký kůň"},
			{"action":"append","text":"## 完了\nUnicode działa."}
		]
	}`)
	res, err := NewEditDocx(dir).Execute(t.Context(), args)
	if err != nil || res.Err != nil {
		t.Fatalf("batch failed: result=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Text, "Applied 4 Word operation(s) in one atomic batch") {
		t.Fatalf("unexpected batch result: %s", res.Text)
	}
	plain := readDocxText(t, dir, "batch.docx")
	for _, want := range []string{"Pchnąć w tę łódź", "ąćęłńóśźż", "Příliš žluťoučký kůň", "完了"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("batch output lost %q: %s", want, plain)
		}
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("single original backup missing: %v", err)
	}
	if !bytes.Equal(backup, original) {
		t.Fatal("batch backup is not the original document")
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".batch.docx.batch-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("batch leaked working files: matches=%v err=%v", matches, err)
	}
}

func TestEditDocx_BatchFailureLeavesOriginalByteIdentical(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "atomic.docx", []string{"Oryginał ąćęłńóśźż."})
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{
		"path":"atomic.docx","action":"batch","operations":[
			{"action":"replace_at","source":"/body/p[1]","text":"Ta zmiana nie może wyciec."},
			{"action":"format_at","source":"/body/p[999]","bold":true}
		]
	}`)
	res, err := NewEditDocx(dir).Execute(t.Context(), args)
	if err == nil && res.Err == nil {
		t.Fatal("invalid operation should fail the whole batch")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("failed batch modified the original document")
	}
	if _, statErr := os.Stat(path + ".bak"); !os.IsNotExist(statErr) {
		t.Fatalf("failed batch should not create an original backup: %v", statErr)
	}
}

func TestEditDocx_BatchAvoidsModelRoundTripsAndFinishesQuickly(t *testing.T) {
	dir := t.TempDir()
	var text strings.Builder
	var operations strings.Builder
	operations.WriteByte('[')
	for i := 0; i < 40; i++ {
		if i > 0 {
			text.WriteByte(' ')
			operations.WriteByte(',')
		}
		fmt.Fprintf(&text, "pole-%02d", i)
		fmt.Fprintf(&operations, `{"action":"replace","find":"pole-%02d","replace":"wartość-%02d"}`, i, i)
	}
	operations.WriteByte(']')
	writeTestDocx(t, dir, "speed.docx", []string{text.String()})
	args := json.RawMessage(`{"path":"speed.docx","action":"batch","operations":` + operations.String() + `}`)
	started := time.Now()
	res, err := NewEditDocx(dir).Execute(t.Context(), args)
	if err != nil || res.Err != nil {
		t.Fatalf("batch failed: result=%+v err=%v", res, err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("40-operation local batch took %s", elapsed)
	}
	if got := readDocxText(t, dir, "speed.docx"); !strings.Contains(got, "wartość-39") {
		t.Fatalf("batch did not reach final operation: %s", got)
	}
}
