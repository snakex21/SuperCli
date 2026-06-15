// edit_docx.go implements the edit_docx tool: pure-Go editing
// of Word .docx files (replace text, append paragraphs, create
// new documents). A .docx is a zip archive whose main content
// is word/document.xml; the tool only rewrites that one entry
// and copies every other zip entry byte-for-byte, so styles,
// images, headers/footers and metadata survive untouched.
//
// Editing strategy: we do NOT re-encode the whole XML tree
// (encoding/xml round-trips mangle Word's namespaces).
// Instead we splice bytes:
//
//   - replace: scan word/document.xml for <w:p>...</w:p>
//     spans. For each paragraph, concatenate the text of all
//     its runs (so a search string split across runs is still
//     found). If the paragraph's text contains the target, the
//     paragraph is rewritten as a single merged run carrying
//     the paragraph's properties (w:pPr) and the FIRST run's
//     formatting (w:rPr). Untouched paragraphs keep their
//     exact bytes. LIMITATION (documented in the tool
//     description): in a paragraph that matched, per-run
//     formatting changes mid-paragraph (e.g. one bold word)
//     are normalized to the first run's formatting.
//
//   - append: new paragraph XML is spliced in just before
//     <w:sectPr> (or </w:body>).
//
//   - create: a minimal valid .docx (content types, rels,
//     styles with Heading 1-3, document.xml) is built from
//     scratch.
//
// Markdown-ish input: lines starting with "# ", "## ", "### "
// become Heading1/2/3; everything else is a normal paragraph.
// Blank lines become empty paragraphs.
//
// Safety: writes go to a temp file which is swapped in only
// when complete, and an existing file is copied to
// "<name>.docx.bak" before being replaced.
package office

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const docxDocumentEntry = "word/document.xml"

// EditDocxTool edits or creates .docx files.
type EditDocxTool struct {
	BaseDir      string
	MaxDocxBytes int64
}

// NewEditDocx returns an EditDocxTool rooted at baseDir.
func NewEditDocx(baseDir string) *EditDocxTool {
	if baseDir == "" {
		baseDir = "."
	}
	return &EditDocxTool{BaseDir: baseDir, MaxDocxBytes: DefaultMaxDocxBytes}
}

// Spec returns the Tool descriptor.
func (t *EditDocxTool) Spec() Tool {
	return Tool{
		Name: "edit_docx",
		Description: "Edit or create a Word .docx file. Pure Go, no Word required. " +
			"Use this when the user wants to change a Word document: fix wording, do a find-and-replace, " +
			"add paragraphs or headings at the end, or create a brand-new document from text. " +
			"Actions: 'replace' (find/replace across the whole document; finds text even when Word split it " +
			"across formatting runs), 'append' (add paragraphs at the end, optional style: Heading1-3 or bold), " +
			"'create' (new .docx from text; lines starting with '# ', '## ', '### ' become headings). " +
			"Safety: before an existing file is changed, a backup copy is saved next to it as '<name>.bak', " +
			"and the write is atomic — tell the user the backup exists if they want to undo. " +
			"Everything not edited (images, styles, headers, tables) is preserved byte-for-byte. " +
			"LIMITATION: in a paragraph where 'replace' matched, mixed per-word formatting is normalized to " +
			"the paragraph's first run's formatting. Do NOT use this for spreadsheets (use edit_xlsx), " +
			"PDFs (cannot be edited), or plain text files (use the line-edit tools). " +
			"Do NOT use 'create' to overwrite an existing file the user did not ask to replace.",
		Schema: `{
  "type": "object",
  "properties": {
    "path":    {"type": "string", "description": "Path to the .docx file (relative paths resolve against the working directory)."},
    "action":  {"type": "string", "enum": ["replace", "append", "create"], "description": "What to do."},
    "find":    {"type": "string", "description": "replace: the exact text to find (case-sensitive)."},
    "replace": {"type": "string", "description": "replace: the replacement text (may be empty to delete)."},
    "text":    {"type": "string", "description": "append/create: the content. One paragraph per line; '# ', '## ', '### ' prefixes become headings."},
    "style":   {"type": "string", "description": "append: optional style for all appended paragraphs: 'Heading1', 'Heading2', 'Heading3', 'bold', or 'normal' (default)."}
  },
  "required": ["path", "action"]
}`,
		Fn: t.Execute,
	}
}

type editDocxArgs struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	Find    string `json:"find"`
	Replace string `json:"replace"`
	Text    string `json:"text"`
	Style   string `json:"style"`
}

// Execute dispatches on action.
func (t *EditDocxTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var p editDocxArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return Result{Err: fmt.Errorf("edit_docx: bad args: %w", err)}, err
	}
	full, err := resolveSandboxed(t.BaseDir, p.Path)
	if err != nil {
		err = fmt.Errorf("edit_docx: %w", err)
		return Result{Err: err}, err
	}
	switch p.Action {
	case "replace":
		return t.doReplace(full, p)
	case "append":
		return t.doAppend(full, p)
	case "create":
		return t.doCreate(full, p)
	default:
		err := fmt.Errorf("edit_docx: unknown action %q (want replace|append|create)", p.Action)
		return Result{Err: err}, err
	}
}

func (t *EditDocxTool) loadDocumentXML(full string) ([]byte, error) {
	info, err := os.Stat(full)
	if err != nil {
		return nil, fmt.Errorf("stat %q: %w", full, err)
	}
	if info.Size() > t.MaxDocxBytes {
		return nil, fmt.Errorf("file too large: %d > %d", info.Size(), t.MaxDocxBytes)
	}
	return readZipEntry(full, docxDocumentEntry, t.MaxDocxBytes)
}

func (t *EditDocxTool) doReplace(full string, p editDocxArgs) (Result, error) {
	if p.Find == "" {
		err := fmt.Errorf("edit_docx replace: 'find' is required and must be non-empty")
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	newDoc, count, err := docxReplaceAll(doc, p.Find, p.Replace)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	if count == 0 {
		return Result{Text: fmt.Sprintf("No occurrences of %q found in %s. Nothing was changed (no backup created).", p.Find, full)}, nil
	}
	backup, err := editZipEntryInPlace(full, docxDocumentEntry, newDoc)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Replaced %d occurrence(s) of %q in %s. Backup of the original saved as %s.", count, p.Find, full, backup)}, nil
}

func (t *EditDocxTool) doAppend(full string, p editDocxArgs) (Result, error) {
	if strings.TrimSpace(p.Text) == "" {
		err := fmt.Errorf("edit_docx append: 'text' is required")
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	paraXML, nParas := buildParagraphsXML(p.Text, p.Style)
	newDoc, err := docxInsertBeforeBodyEnd(doc, paraXML)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	backup, err := editZipEntryInPlace(full, docxDocumentEntry, newDoc)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Appended %d paragraph(s) to %s. Backup of the original saved as %s.", nParas, full, backup)}, nil
}

func (t *EditDocxTool) doCreate(full string, p editDocxArgs) (Result, error) {
	if _, err := os.Lstat(full); err == nil {
		err := fmt.Errorf("edit_docx create: %q already exists. Refusing to overwrite — ask the user whether to replace it, pick a different name, or use action 'append'/'replace' to modify it", full)
		return Result{Err: err}, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return Result{Err: fmt.Errorf("edit_docx: create parent folder: %w", err)}, err
	}
	paraXML, nParas := buildParagraphsXML(p.Text, "")
	tmp := filepath.Join(filepath.Dir(full), "."+filepath.Base(full)+".tmp")
	if err := writeMinimalDocx(tmp, paraXML); err != nil {
		os.Remove(tmp)
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	if _, err := backupAndReplace(full, tmp); err != nil {
		os.Remove(tmp)
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Created %s with %d paragraph(s).", full, nParas)}, nil
}

// ---------------------------------------------------------------------------
// replace: byte-splicing over word/document.xml
// ---------------------------------------------------------------------------

// docxReplaceAll finds every <w:p> paragraph whose concatenated
// run text contains find, and rewrites those paragraphs with
// the replacement applied. Returns the new document bytes and
// the total occurrence count.
func docxReplaceAll(doc []byte, find, repl string) ([]byte, int, error) {
	var out bytes.Buffer
	total := 0
	pos := 0
	for {
		open, openEnd, selfClosing := findNextParagraph(doc, pos)
		if open < 0 {
			out.Write(doc[pos:])
			break
		}
		if selfClosing {
			out.Write(doc[pos:openEnd])
			pos = openEnd
			continue
		}
		closeIdx := bytes.Index(doc[openEnd:], []byte("</w:p>"))
		if closeIdx < 0 {
			return nil, 0, fmt.Errorf("malformed document.xml: unclosed <w:p>")
		}
		paraEnd := openEnd + closeIdx + len("</w:p>")
		frag := doc[open:paraEnd]

		text, err := docxParagraphText(frag)
		if err != nil {
			return nil, 0, err
		}
		n := strings.Count(text, find)
		if n == 0 {
			out.Write(doc[pos:paraEnd])
			pos = paraEnd
			continue
		}
		total += n
		newText := strings.ReplaceAll(text, find, repl)
		rebuilt := rebuildParagraph(frag, doc[open:openEnd], newText)
		out.Write(doc[pos:open])
		out.Write(rebuilt)
		pos = paraEnd
	}
	return out.Bytes(), total, nil
}

// findNextParagraph locates the next top-level paragraph open
// tag at or after pos. Returns (start, end-of-open-tag,
// selfClosing). Returns start = -1 when no more paragraphs.
// It is careful not to match <w:pPr>, <w:pStyle>, etc.: the
// byte after "<w:p" must be '>', ' ', or '/'.
func findNextParagraph(doc []byte, pos int) (int, int, bool) {
	for {
		i := bytes.Index(doc[pos:], []byte("<w:p"))
		if i < 0 {
			return -1, 0, false
		}
		start := pos + i
		after := start + len("<w:p")
		if after >= len(doc) {
			return -1, 0, false
		}
		c := doc[after]
		if c != '>' && c != ' ' && c != '/' {
			pos = after
			continue
		}
		gt := bytes.IndexByte(doc[start:], '>')
		if gt < 0 {
			return -1, 0, false
		}
		openEnd := start + gt + 1
		selfClosing := doc[start+gt-1] == '/'
		return start, openEnd, selfClosing
	}
}

// docxParagraphText extracts the concatenated visible text of
// one paragraph fragment, joining ALL runs (so a search string
// Word split across runs is still found). <w:br/> contributes
// "\n" and <w:tab/> contributes "\t".
func docxParagraphText(frag []byte) (string, error) {
	// Wrap so the w: prefix is declared for the decoder.
	wrapped := append([]byte(`<root xmlns:w="`+wordProcessingNS+`">`), frag...)
	wrapped = append(wrapped, []byte("</root>")...)
	dec := xml.NewDecoder(bytes.NewReader(wrapped))
	var sb strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse paragraph: %w", err)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "pPr", "rPr":
				// Skip property blocks entirely:
				// they never contain visible text.
				if err := dec.Skip(); err != nil {
					return "", err
				}
			case "t":
				var tt struct {
					Text string `xml:",chardata"`
				}
				if err := dec.DecodeElement(&tt, &se); err != nil {
					return "", err
				}
				sb.WriteString(tt.Text)
			case "br":
				sb.WriteString("\n")
				if err := dec.Skip(); err != nil {
					return "", err
				}
			case "tab":
				sb.WriteString("\t")
				if err := dec.Skip(); err != nil {
					return "", err
				}
			}
		}
	}
	return sb.String(), nil
}

// rebuildParagraph produces a replacement <w:p> for a matched
// paragraph: original open tag, original <w:pPr> block (if
// any), then ONE merged run carrying the first run's <w:rPr>
// (if any) and the new text. Newlines in the new text become
// <w:br/>, tabs become <w:tab/>.
func rebuildParagraph(frag, openTag []byte, newText string) []byte {
	var out bytes.Buffer
	out.Write(openTag)

	// Preserve the paragraph-properties block verbatim.
	pPr := extractBlock(frag, "w:pPr")
	out.Write(pPr)

	// First run's properties, searched AFTER the pPr block
	// (a pPr can itself contain a rPr for the paragraph
	// mark, which we must not mistake for run formatting).
	rest := frag[len(openTag):]
	if len(pPr) > 0 {
		if idx := bytes.Index(rest, pPr); idx >= 0 {
			rest = rest[idx+len(pPr):]
		}
	}
	rPr := extractBlock(rest, "w:rPr")

	out.WriteString("<w:r>")
	out.Write(rPr)
	parts := strings.Split(newText, "\n")
	for i, part := range parts {
		if i > 0 {
			out.WriteString("<w:br/>")
		}
		writeRunText(&out, part)
	}
	out.WriteString("</w:r></w:p>")
	return out.Bytes()
}

// writeRunText emits the text of one line as <w:t> elements
// with <w:tab/> for tabs.
func writeRunText(out *bytes.Buffer, line string) {
	segs := strings.Split(line, "\t")
	for i, seg := range segs {
		if i > 0 {
			out.WriteString("<w:tab/>")
		}
		if seg == "" {
			continue
		}
		out.WriteString(`<w:t xml:space="preserve">`)
		out.WriteString(xmlEscapeText(seg))
		out.WriteString("</w:t>")
	}
}

// extractBlock returns the first "<tag ...>...</tag>" or
// "<tag .../>" span in b, or nil when absent.
func extractBlock(b []byte, tag string) []byte {
	open := []byte("<" + tag)
	i := bytes.Index(b, open)
	if i < 0 {
		return nil
	}
	after := i + len(open)
	if after >= len(b) || (b[after] != '>' && b[after] != ' ' && b[after] != '/') {
		// e.g. matched <w:pPrChange — search again past it.
		rest := extractBlock(b[after:], tag)
		return rest
	}
	gt := bytes.IndexByte(b[i:], '>')
	if gt < 0 {
		return nil
	}
	if b[i+gt-1] == '/' {
		return b[i : i+gt+1]
	}
	closeTag := []byte("</" + tag + ">")
	j := bytes.Index(b[i:], closeTag)
	if j < 0 {
		return nil
	}
	return b[i : i+j+len(closeTag)]
}

// ---------------------------------------------------------------------------
// append / create
// ---------------------------------------------------------------------------

// buildParagraphsXML converts markdown-ish text into
// WordprocessingML paragraph XML. Each input line is one
// paragraph; "# ", "## ", "### " prefixes select Heading1-3.
// style (append only) forces a style for non-heading lines:
// Heading1-3, bold, or normal. Returns (xml, paragraphCount).
func buildParagraphsXML(text, style string) ([]byte, int) {
	var out bytes.Buffer
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	n := 0
	for _, line := range lines {
		heading := ""
		body := line
		switch {
		case strings.HasPrefix(line, "### "):
			heading, body = "Heading3", line[4:]
		case strings.HasPrefix(line, "## "):
			heading, body = "Heading2", line[3:]
		case strings.HasPrefix(line, "# "):
			heading, body = "Heading1", line[2:]
		}
		bold := false
		if heading == "" {
			switch strings.ToLower(strings.TrimSpace(style)) {
			case "heading1":
				heading = "Heading1"
			case "heading2":
				heading = "Heading2"
			case "heading3":
				heading = "Heading3"
			case "bold":
				bold = true
			}
		}
		// Whole-line **bold** markdown.
		if trimmed := strings.TrimSpace(body); strings.HasPrefix(trimmed, "**") && strings.HasSuffix(trimmed, "**") && len(trimmed) > 4 {
			bold = true
			body = strings.TrimSuffix(strings.TrimPrefix(trimmed, "**"), "**")
		}
		out.WriteString("<w:p>")
		if heading != "" {
			out.WriteString(`<w:pPr><w:pStyle w:val="` + heading + `"/></w:pPr>`)
		}
		if body != "" {
			out.WriteString("<w:r>")
			if bold {
				out.WriteString("<w:rPr><w:b/></w:rPr>")
			}
			writeRunText(&out, body)
			out.WriteString("</w:r>")
		}
		out.WriteString("</w:p>")
		n++
	}
	return out.Bytes(), n
}

// docxInsertBeforeBodyEnd splices paraXML into the document
// just before <w:sectPr> (the section properties must stay
// last in the body) or, absent that, before </w:body>.
func docxInsertBeforeBodyEnd(doc, paraXML []byte) ([]byte, error) {
	insertAt := bytes.LastIndex(doc, []byte("<w:sectPr"))
	if insertAt < 0 {
		insertAt = bytes.LastIndex(doc, []byte("</w:body>"))
	}
	if insertAt < 0 {
		return nil, fmt.Errorf("malformed document.xml: no </w:body>")
	}
	out := make([]byte, 0, len(doc)+len(paraXML))
	out = append(out, doc[:insertAt]...)
	out = append(out, paraXML...)
	out = append(out, doc[insertAt:]...)
	return out, nil
}

// writeMinimalDocx writes a complete minimal .docx at path
// containing the given body paragraph XML. Includes a basic
// styles part so Heading1-3 render styled in Word.
func writeMinimalDocx(path string, paraXML []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	body := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="` + wordProcessingNS + `"><w:body>` +
		string(paraXML) +
		`</w:body></w:document>`
	entries := []struct{ name, data string }{
		{"[Content_Types].xml", docxContentTypes},
		{"_rels/.rels", docxRels},
		{"word/_rels/document.xml.rels", docxDocumentRels},
		{"word/styles.xml", docxStyles},
		{docxDocumentEntry, body},
	}
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(e.data)); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

const docxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
</Types>`

const docxRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

const docxDocumentRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`

const docxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
  <w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:before="240" w:after="120"/></w:pPr><w:rPr><w:b/><w:sz w:val="32"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:before="200" w:after="100"/></w:pPr><w:rPr><w:b/><w:sz w:val="28"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:before="160" w:after="80"/></w:pPr><w:rPr><w:b/><w:sz w:val="26"/></w:rPr></w:style>
</w:styles>`
