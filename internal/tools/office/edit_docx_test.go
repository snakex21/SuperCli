package office

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runEditDocx(t *testing.T, tool *EditDocxTool, args string) Result {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("edit_docx error: %v", err)
	}
	return res
}

func readDocxText(t *testing.T, dir, name string) string {
	t.Helper()
	rd := NewReadDocx(dir, 0)
	res, err := rd.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q}`, name)))
	if err != nil {
		t.Fatalf("read_docx error: %v", err)
	}
	return res.Text
}

func TestEditDocx_ReplaceSimple(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"Hello world.", "Goodbye world."})
	tool := NewEditDocx(dir)

	res := runEditDocx(t, tool, `{"path":"a.docx","action":"replace","find":"world","replace":"team"}`)
	if !strings.Contains(res.Text, "Replaced 2 occurrence(s)") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
	got := readDocxText(t, dir, "a.docx")
	if !strings.Contains(got, "Hello team.") || !strings.Contains(got, "Goodbye team.") {
		t.Fatalf("round-trip text wrong: %q", got)
	}
	// Backup must exist with the ORIGINAL content.
	bak := filepath.Join(dir, "a.docx.bak")
	if _, err := os.Stat(bak); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if got := readDocxText(t, dir, "a.docx.bak"); !strings.Contains(got, "Hello world.") {
		t.Fatalf("backup content wrong: %q", got)
	}
}

// Text split across multiple runs in one paragraph must still
// be found (the run-merge behavior).
func TestEditDocx_ReplaceAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "split.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	fw, _ := w.Create("[Content_Types].xml")
	fw.Write([]byte(`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`))
	fw, _ = w.Create("word/document.xml")
	// "magic word" split as "mag" + "ic wo" + "rd", second run bold.
	fw.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="` + wordProcessingNS + `"><w:body>` +
		`<w:p><w:r><w:t>mag</w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t>ic wo</w:t></w:r><w:r><w:t>rd here</w:t></w:r></w:p>` +
		`</w:body></w:document>`))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	tool := NewEditDocx(dir)
	res := runEditDocx(t, tool, `{"path":"split.docx","action":"replace","find":"magic word","replace":"plain text"}`)
	if !strings.Contains(res.Text, "Replaced 1 occurrence(s)") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
	got := readDocxText(t, dir, "split.docx")
	if !strings.Contains(got, "plain text here") {
		t.Fatalf("cross-run replace failed: %q", got)
	}
	doc, err := readZipEntry(path, docxDocumentEntry, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(doc, []byte(`<w:rPr><w:b/></w:rPr>`)) {
		t.Fatalf("mixed run formatting was discarded: %s", doc)
	}
	if got := bytes.Count(doc, []byte("<w:r>")); got != 3 {
		t.Fatalf("replace merged runs: got %d runs, want 3", got)
	}
}

func TestEditDocx_ReplaceKeepsAffectedRunStyle(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "styled.docx", []string{"Hello world!"})
	doc, err := readZipEntry(path, docxDocumentEntry, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	doc = bytes.Replace(doc,
		[]byte(`<w:r><w:t>Hello world!</w:t></w:r>`),
		[]byte(`<w:r><w:t>Hello </w:t></w:r><w:r><w:rPr><w:b/><w:color w:val="C00000"/></w:rPr><w:t>world</w:t></w:r><w:r><w:t>!</w:t></w:r>`), 1)
	if _, err := editZipEntryInPlace(path, docxDocumentEntry, doc); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")

	runEditDocx(t, NewEditDocx(dir), `{"path":"styled.docx","action":"replace","find":"world","replace":"team"}`)
	got, err := readZipEntry(path, docxDocumentEntry, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`<w:rPr><w:b/><w:color w:val="C00000"/></w:rPr><w:t xml:space="preserve">team</w:t>`)) {
		t.Fatalf("replacement did not retain the affected run style: %s", got)
	}
}

func TestEditDocx_ReplaceNoMatchNoBackup(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"Hello."})
	tool := NewEditDocx(dir)
	res := runEditDocx(t, tool, `{"path":"a.docx","action":"replace","find":"zzz","replace":"y"}`)
	if !strings.Contains(res.Text, "No occurrences") {
		t.Fatalf("unexpected: %q", res.Text)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.docx.bak")); err == nil {
		t.Fatal("backup should not be created when nothing changed")
	}
}

// Untouched zip entries must survive byte-for-byte.
func TestEditDocx_PreservesOtherEntries(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "a.docx", []string{"Hello world."})

	// Append a custom entry to the archive to track.
	// Rebuild zip with an extra entry.
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(strings.NewReader(string(orig)), int64(len(orig)))
	if err != nil {
		t.Fatal(err)
	}
	f2, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f2)
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		w, _ := zw.Create(zf.Name)
		buf := make([]byte, zf.UncompressedSize64)
		if _, err := io.ReadFull(rc, buf); err != nil {
			t.Fatal(err)
		}
		w.Write(buf)
		rc.Close()
	}
	marker := []byte("custom-marker-payload-12345")
	w, _ := zw.Create("docProps/custom.xml")
	w.Write(marker)
	zw.Close()
	f2.Close()

	tool := NewEditDocx(dir)
	runEditDocx(t, tool, `{"path":"a.docx","action":"replace","find":"world","replace":"team"}`)

	got, err := readZipEntry(path, "docProps/custom.xml", 1<<20)
	if err != nil {
		t.Fatalf("custom entry lost: %v", err)
	}
	if string(got) != string(marker) {
		t.Fatalf("custom entry corrupted: %q", got)
	}
}

func TestEditDocx_Append(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"Intro."})
	tool := NewEditDocx(dir)
	res := runEditDocx(t, tool, `{"path":"a.docx","action":"append","text":"# Summary\nAll done.","style":""}`)
	if !strings.Contains(res.Text, "Appended 2 paragraph(s)") {
		t.Fatalf("unexpected: %q", res.Text)
	}
	got := readDocxText(t, dir, "a.docx")
	want := []string{"Intro.", "Summary", "All done."}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in %q", w, got)
		}
	}
	// Order: Intro before Summary.
	if strings.Index(got, "Intro.") > strings.Index(got, "Summary") {
		t.Fatalf("appended text not at end: %q", got)
	}
}

func TestEditDocx_AppendMatchesDocumentBodyStyleByDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "styled.docx", []string{"Styled body."})
	doc, err := readZipEntry(path, docxDocumentEntry, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	doc = bytes.Replace(doc, []byte(`<w:p><w:r><w:t>Styled body.</w:t></w:r></w:p>`),
		[]byte(`<w:p><w:pPr><w:spacing w:after="240" w:line="300"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Aptos"/><w:color w:val="123456"/></w:rPr><w:t>Styled body.</w:t></w:r></w:p>`), 1)
	if _, err := editZipEntryInPlace(path, docxDocumentEntry, doc); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")

	runEditDocx(t, NewEditDocx(dir), `{"path":"styled.docx","action":"append","text":"New body."}`)
	got, err := readZipEntry(path, docxDocumentEntry, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(got, []byte(`<w:spacing w:after="240" w:line="300"/>`)) != 2 ||
		bytes.Count(got, []byte(`<w:rFonts w:ascii="Aptos"/><w:color w:val="123456"/>`)) != 2 {
		t.Fatalf("appended paragraph did not inherit document formatting: %s", got)
	}
}

func TestEditDocx_AppendPlainOptOut(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "styled.docx", []string{"Styled body."})
	doc, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	doc = bytes.Replace(doc, []byte(`<w:p><w:r><w:t>Styled body.</w:t></w:r></w:p>`),
		[]byte(`<w:p><w:pPr><w:spacing w:after="240"/></w:pPr><w:r><w:t>Styled body.</w:t></w:r></w:p>`), 1)
	if _, err := editZipEntryInPlace(path, docxDocumentEntry, doc); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")

	runEditDocx(t, NewEditDocx(dir), `{"path":"styled.docx","action":"append","text":"Plain body.","style_mode":"plain"}`)
	got, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	if bytes.Count(got, []byte(`<w:spacing w:after="240"/>`)) != 1 {
		t.Fatalf("plain opt-out unexpectedly cloned formatting: %s", got)
	}
}

func TestEditDocx_AppendRejectsUnknownStyleMode(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"Body."})
	_, err := NewEditDocx(dir).Execute(context.Background(), json.RawMessage(`{"path":"a.docx","action":"append","text":"More.","style_mode":"guess"}`))
	if err == nil || !strings.Contains(err.Error(), "want match|plain") {
		t.Fatalf("want style_mode validation error, got %v", err)
	}
}

func TestReadDocx_SelectorsExposeStableParagraphPaths(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"First.", "Second."})
	res, err := NewReadDocx(dir, 0).Execute(context.Background(), json.RawMessage(`{"path":"a.docx","selectors":true}`))
	if err != nil || res.Err != nil {
		t.Fatalf("read selectors err=%v result=%v", err, res.Err)
	}
	for _, want := range []string{"/body/p[1] [style=Normal] First.", "/body/p[2] [style=Normal] Second."} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("selector output missing %q: %s", want, res.Text)
		}
	}
}

func TestReadDocx_SelectorsExposeCompactDirectFormatting(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "styled.docx", []string{"Styled."})
	doc, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	doc = bytes.Replace(doc, []byte(`<w:p><w:r><w:t>Styled.</w:t></w:r></w:p>`),
		[]byte(`<w:p><w:pPr><w:jc w:val="center"/><w:spacing w:after="240"/></w:pPr><w:r><w:rPr><w:rFonts w:ascii="Aptos"/><w:sz w:val="24"/><w:b/><w:color w:val="336699"/></w:rPr><w:t>Styled.</w:t></w:r></w:p>`), 1)
	if _, err := editZipEntryInPlace(path, docxDocumentEntry, doc); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")
	res, err := NewReadDocx(dir, 0).Execute(context.Background(), json.RawMessage(`{"path":"styled.docx","selectors":true,"formatting":true}`))
	if err != nil || res.Err != nil {
		t.Fatalf("read selectors err=%v result=%v", err, res.Err)
	}
	for _, want := range []string{"align=center", "font=Aptos", "size=12pt", "bold", "color=#336699", "after=240"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("formatting output missing %q: %s", want, res.Text)
		}
	}
}

func TestReadDocx_TableParagraphSelectorsAndReplaceAt(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "table.docx", []string{"Before table."})
	doc, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	table := []byte(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/></w:tblPr><w:tr>` +
		`<w:tc><w:tcPr><w:tcW w:w="3000" w:type="dxa"/></w:tcPr><w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:rPr><w:b/></w:rPr><w:t>Alpha</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:p><w:r><w:t>Beta</w:t></w:r></w:p></w:tc>` +
		`</w:tr></w:tbl>`)
	doc, err := docxInsertBeforeBodyEnd(doc, table)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := editZipEntryInPlace(path, docxDocumentEntry, doc); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")

	read, err := NewReadDocx(dir, 0).Execute(context.Background(), json.RawMessage(`{"path":"table.docx","selectors":true,"formatting":true}`))
	if err != nil || read.Err != nil {
		t.Fatalf("read table selectors err=%v result=%v", err, read.Err)
	}
	for _, want := range []string{
		`/body/tbl[1]/tr[1]/tc[1]/p[1] [style=Normal align=center bold] Alpha`,
		`/body/tbl[1]/tr[1]/tc[2]/p[1] [style=Normal] Beta`,
	} {
		if !strings.Contains(read.Text, want) {
			t.Fatalf("table selector output missing %q: %s", want, read.Text)
		}
	}

	runEditDocx(t, NewEditDocx(dir), `{"path":"table.docx","action":"replace_at","source":"/body/tbl[1]/tr[1]/tc[1]/p[1]","text":"Gamma"}`)
	got, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	if !bytes.Contains(got, []byte(`<w:tcW w:w="3000" w:type="dxa"/>`)) || !bytes.Contains(got, []byte(`<w:jc w:val="center"/>`)) || !bytes.Contains(got, []byte(`<w:b/>`)) {
		t.Fatalf("table replace_at lost cell/paragraph/run style: %s", got)
	}
	plain := readDocxText(t, dir, "table.docx")
	if !strings.Contains(plain, "Gamma | Beta") || strings.Contains(plain, "Alpha") {
		t.Fatalf("table edit changed wrong content: %s", plain)
	}
}

func TestReadDocx_TableSelectorsIgnoreNestedTableCoordinates(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "nested.docx", []string{"Body."})
	doc, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	table := []byte(`<w:tbl><w:tr><w:tc>` +
		`<w:p><w:r><w:t>Outer one</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Nested hidden selector</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`<w:p><w:r><w:t>Outer two</w:t></w:r></w:p>` +
		`</w:tc></w:tr></w:tbl>`)
	doc, _ = docxInsertBeforeBodyEnd(doc, table)
	if _, err := editZipEntryInPlace(path, docxDocumentEntry, doc); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")
	read, err := NewReadDocx(dir, 0).Execute(context.Background(), json.RawMessage(`{"path":"nested.docx","selectors":true}`))
	if err != nil || read.Err != nil {
		t.Fatalf("read nested selectors err=%v result=%v", err, read.Err)
	}
	if !strings.Contains(read.Text, `/body/tbl[1]/tr[1]/tc[1]/p[1] [style=Normal] Outer one`) ||
		!strings.Contains(read.Text, `/body/tbl[1]/tr[1]/tc[1]/p[2] [style=Normal] Outer two`) {
		t.Fatalf("outer table selectors missing: %s", read.Text)
	}
	if strings.Contains(read.Text, "Nested hidden selector") {
		t.Fatalf("nested table received ambiguous top-level selector: %s", read.Text)
	}
}

func TestReadAndReplaceDocxHeaderFooterParts(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "stories.docx", []string{"Body stays."})
	header := []byte(`<?xml version="1.0" encoding="UTF-8"?><w:hdr xmlns:w="` + wordProcessingNS + `"><w:p><w:r><w:rPr><w:i/></w:rPr><w:t>Old header</w:t></w:r></w:p></w:hdr>`)
	footer := []byte(`<?xml version="1.0" encoding="UTF-8"?><w:ftr xmlns:w="` + wordProcessingNS + `"><w:p><w:r><w:t>Old footer</w:t></w:r></w:p></w:ftr>`)
	if _, err := editZipEntriesInPlace(path, map[string][]byte{"word/header1.xml": header, "word/footer1.xml": footer}); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")

	read, err := NewReadDocx(dir, 0).Execute(context.Background(), json.RawMessage(`{"path":"stories.docx","include_headers":true,"include_footers":true}`))
	if err != nil || read.Err != nil {
		t.Fatalf("read stories err=%v result=%v", err, read.Err)
	}
	for _, want := range []string{"== word/header1.xml ==", "Old header", "== word/footer1.xml ==", "Old footer"} {
		if !strings.Contains(read.Text, want) {
			t.Fatalf("story output missing %q: %s", want, read.Text)
		}
	}

	res := runEditDocx(t, NewEditDocx(dir), `{"path":"stories.docx","action":"replace","find":"Old","replace":"New","include_headers":true,"include_footers":true}`)
	if !strings.Contains(res.Text, "2 occurrence") || !strings.Contains(res.Text, "2 additional") {
		t.Fatalf("unexpected multi-story result: %s", res.Text)
	}
	newHeader, _ := readZipEntry(path, "word/header1.xml", 1<<20)
	newFooter, _ := readZipEntry(path, "word/footer1.xml", 1<<20)
	if !bytes.Contains(newHeader, []byte("New header")) || !bytes.Contains(newFooter, []byte("New footer")) || !bytes.Contains(newHeader, []byte(`<w:i/>`)) {
		t.Fatalf("story replace failed or lost formatting: header=%s footer=%s", newHeader, newFooter)
	}
	body := readDocxText(t, dir, "stories.docx")
	if body != "Body stays.\n" {
		t.Fatalf("story replace changed document body: %q", body)
	}
}

func TestEditDocx_CloneSelectorPreservesStyleAndPlacement(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "styled.docx", []string{"Template.", "Tail."})
	doc, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	doc = bytes.Replace(doc, []byte(`<w:p><w:r><w:t>Template.</w:t></w:r></w:p>`),
		[]byte(`<w:p><w:pPr><w:spacing w:after="180"/></w:pPr><w:r><w:rPr><w:color w:val="336699"/></w:rPr><w:t>Template.</w:t></w:r></w:p>`), 1)
	if _, err := editZipEntryInPlace(path, docxDocumentEntry, doc); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")

	res := runEditDocx(t, NewEditDocx(dir), `{"path":"styled.docx","action":"clone","source":"/body/p[1]","after":"/body/p[2]","text":"Matched copy."}`)
	if !strings.Contains(res.Text, "Cloned /body/p[1]") {
		t.Fatalf("unexpected clone result: %s", res.Text)
	}
	got, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	if bytes.Count(got, []byte(`<w:spacing w:after="180"/>`)) != 2 || bytes.Count(got, []byte(`<w:color w:val="336699"/>`)) != 2 {
		t.Fatalf("clone lost source style: %s", got)
	}
	plain := readDocxText(t, dir, "styled.docx")
	if !(strings.Index(plain, "Tail.") < strings.Index(plain, "Matched copy.")) {
		t.Fatalf("clone placement is wrong: %s", plain)
	}
}

func TestEditDocx_CloneRejectsBadSelector(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"One."})
	_, err := NewEditDocx(dir).Execute(context.Background(), json.RawMessage(`{"path":"a.docx","action":"clone","source":"/body/p[9]"}`))
	if err == nil || !strings.Contains(err.Error(), "outside 1..1") {
		t.Fatalf("want selector bounds error, got %v", err)
	}
}

func TestEditDocx_ReplaceAtPreservesStyleAndOnlyTouchesSelectedParagraph(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "styled.docx", []string{"Same.", "Same."})
	doc, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	doc = bytes.Replace(doc, []byte(`<w:p><w:r><w:t>Same.</w:t></w:r></w:p>`),
		[]byte(`<w:p><w:pPr><w:jc w:val="right"/></w:pPr><w:r><w:rPr><w:b/><w:color w:val="112233"/></w:rPr><w:t>Same.</w:t></w:r></w:p>`), 1)
	if _, err := editZipEntryInPlace(path, docxDocumentEntry, doc); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")

	runEditDocx(t, NewEditDocx(dir), `{"path":"styled.docx","action":"replace_at","source":"/body/p[1]","text":"Changed."}`)
	got, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	if !bytes.Contains(got, []byte(`<w:jc w:val="right"/>`)) || !bytes.Contains(got, []byte(`<w:color w:val="112233"/>`)) {
		t.Fatalf("replace_at lost formatting: %s", got)
	}
	plain := readDocxText(t, dir, "styled.docx")
	if strings.Count(plain, "Changed.") != 1 || strings.Count(plain, "Same.") != 1 {
		t.Fatalf("replace_at touched wrong paragraphs: %s", plain)
	}
}

func TestEditDocx_DryRunValidatesWithoutWritingOrBackup(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "a.docx", []string{"Original."})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res := runEditDocx(t, NewEditDocx(dir), `{"path":"a.docx","action":"replace_at","source":"/body/p[1]","text":"Preview.","dry_run":true}`)
	if !strings.Contains(res.Text, "Preview only") {
		t.Fatalf("unexpected dry-run result: %s", res.Text)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("dry_run changed the docx")
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("dry_run created backup: %v", err)
	}
}

func TestEditDocx_SuggestAtCreatesMinimalTrackedChangeAndInfrastructure(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "review.docx", []string{"The report is monthly and final."})
	doc, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	doc = bytes.Replace(doc,
		[]byte(`<w:r><w:t>The report is monthly and final.</w:t></w:r>`),
		[]byte(`<w:r w:rsidR="00112233"><w:rPr><w:color w:val="112233"/></w:rPr><w:t>The report is </w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t>monthly</w:t></w:r><w:r><w:t> and final.</w:t></w:r>`), 1)
	if _, err := editZipEntryInPlace(path, docxDocumentEntry, doc); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")

	res := runEditDocx(t, NewEditDocx(dir), `{"path":"review.docx","action":"suggest_at","source":"/body/p[1]","text":"The report is quarterly and final.","author":"Jan Kowalski"}`)
	if !strings.Contains(res.Text, "minimal tracked replacement") {
		t.Fatalf("unexpected result: %s", res.Text)
	}
	got, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	for _, want := range []string{`<w:del `, `<w:delText>month</w:delText>`, `<w:ins `, `<w:t>quarter</w:t>`, `w:author="Jan Kowalski"`, `<w:b/>`} {
		if !bytes.Contains(got, []byte(want)) {
			t.Fatalf("tracked XML missing %q: %s", want, got)
		}
	}
	if bytes.Contains(got, []byte(`<w:delText>The report is monthly and final.</w:delText>`)) {
		t.Fatalf("unchanged text was unnecessarily redlined: %s", got)
	}
	settings, _ := readZipEntry(path, docxSettingsEntry, 1<<20)
	if !bytes.Contains(settings, []byte(`<w:trackRevisions/>`)) {
		t.Fatalf("track revisions setting missing: %s", settings)
	}
	rels, _ := readZipEntry(path, docxRelsEntry, 1<<20)
	types, _ := readZipEntry(path, docxTypesEntry, 1<<20)
	if !bytes.Contains(rels, []byte(`/relationships/settings`)) || !bytes.Contains(types, []byte(`/word/settings.xml`)) {
		t.Fatalf("tracked-change package infrastructure missing: rels=%s types=%s", rels, types)
	}
	if current := readDocxText(t, dir, "review.docx"); !strings.Contains(current, "The report is quarterly and final.") || strings.Contains(current, "monthly") {
		t.Fatalf("current revision rendering is wrong: %q", current)
	}
}

func TestEditDocx_CommentAtCreatesReadableCommentWithoutChangingText(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "comments.docx", []string{"Review this paragraph."})
	res := runEditDocx(t, NewEditDocx(dir), `{"path":"comments.docx","action":"comment_at","source":"/body/p[1]","comment":"Please verify the date.","author":"Anna Nowak"}`)
	if !strings.Contains(res.Text, "Added comment 0") {
		t.Fatalf("unexpected result: %s", res.Text)
	}
	doc, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	comments, _ := readZipEntry(path, docxCommentsEntry, 1<<20)
	for _, want := range []string{`<w:commentRangeStart w:id="0"/>`, `<w:commentRangeEnd w:id="0"/>`, `<w:commentReference w:id="0"/>`} {
		if !bytes.Contains(doc, []byte(want)) {
			t.Fatalf("comment anchor missing %q: %s", want, doc)
		}
	}
	if !bytes.Contains(comments, []byte(`w:author="Anna Nowak"`)) || !bytes.Contains(comments, []byte(`w:initials="AN"`)) || !bytes.Contains(comments, []byte(`Please verify the date.`)) {
		t.Fatalf("comment part is wrong: %s", comments)
	}
	read, err := NewReadDocx(dir, 0).Execute(context.Background(), json.RawMessage(`{"path":"comments.docx","include_comments":true}`))
	if err != nil || read.Err != nil || !strings.Contains(read.Text, `[id=0 author=Anna Nowak] Please verify the date.`) {
		t.Fatalf("comment is not readable: err=%v result=%+v", err, read)
	}
	if !strings.HasPrefix(read.Text, "Review this paragraph.\n") {
		t.Fatalf("comment changed visible document text: %q", read.Text)
	}
	rels, _ := readZipEntry(path, docxRelsEntry, 1<<20)
	types, _ := readZipEntry(path, docxTypesEntry, 1<<20)
	if !bytes.Contains(rels, []byte(`/relationships/comments`)) || !bytes.Contains(types, []byte(`/word/comments.xml`)) {
		t.Fatalf("comment package infrastructure missing: rels=%s types=%s", rels, types)
	}
}

func TestEditDocx_ReviewDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "review.docx", []string{"Original."})
	before, _ := os.ReadFile(path)
	res := runEditDocx(t, NewEditDocx(dir), `{"path":"review.docx","action":"suggest_at","source":"/body/p[1]","text":"Changed.","dry_run":true}`)
	if !strings.Contains(res.Text, "Preview only") {
		t.Fatalf("unexpected dry run: %s", res.Text)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("review dry_run changed the document")
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("review dry_run created backup: %v", err)
	}
}

func TestEditDocx_SuggestAtRejectsComplexParagraph(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "link.docx", []string{"Link text"})
	doc, _ := readZipEntry(path, docxDocumentEntry, 1<<20)
	doc = bytes.Replace(doc, []byte(`<w:r><w:t>Link text</w:t></w:r>`), []byte(`<w:hyperlink r:id="rId9" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:r><w:t>Link text</w:t></w:r></w:hyperlink>`), 1)
	if _, err := editZipEntryInPlace(path, docxDocumentEntry, doc); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".bak")
	_, err := NewEditDocx(dir).Execute(context.Background(), json.RawMessage(`{"path":"link.docx","action":"suggest_at","source":"/body/p[1]","text":"Changed"}`))
	if err == nil || !strings.Contains(err.Error(), "field, link, drawing") {
		t.Fatalf("want safe complex-paragraph refusal, got %v", err)
	}
}

func TestEditDocx_CreateAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditDocx(dir)
	res := runEditDocx(t, tool, `{"path":"new.docx","action":"create","text":"# Title\nFirst paragraph.\n## Section\n**Bold note**"}`)
	if !strings.Contains(res.Text, "Created") {
		t.Fatalf("unexpected: %q", res.Text)
	}
	got := readDocxText(t, dir, "new.docx")
	for _, w := range []string{"Title", "First paragraph.", "Section", "Bold note"} {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in round-trip read: %q", w, got)
		}
	}
}

func TestEditDocx_CreateRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"Existing."})
	tool := NewEditDocx(dir)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.docx","action":"create","text":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want overwrite refusal, got %v", err)
	}
}

func TestEditDocx_SandboxEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditDocx(dir)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"../outside.docx","action":"create","text":"x"}`))
	if err == nil {
		t.Fatal("want sandbox error for path escape")
	}
}

func TestEditDocx_SpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"A & B < C."})
	tool := NewEditDocx(dir)
	runEditDocx(t, tool, `{"path":"a.docx","action":"replace","find":"A & B","replace":"X <> \"Y\""}`)
	got := readDocxText(t, dir, "a.docx")
	if !strings.Contains(got, `X <> "Y" < C.`) {
		t.Fatalf("escaping broken: %q", got)
	}
}

func TestEditDocx_Spec(t *testing.T) {
	spec := NewEditDocx(".").Spec()
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if spec.Name != "edit_docx" {
		t.Fatalf("name: %q", spec.Name)
	}
	if !strings.Contains(spec.Description, "Mixed per-word formatting is retained") {
		t.Error("description must document style-preserving replacement")
	}
}
