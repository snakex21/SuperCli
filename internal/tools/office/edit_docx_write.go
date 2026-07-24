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
//     found), then splice only the affected text nodes. Run and
//     paragraph properties remain byte-for-byte intact. A
//     replacement spanning multiple runs inherits the first
//     affected run's formatting; unaffected text keeps its own.
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
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type docxParagraphTemplate struct {
	frag, openTag []byte
	style         string
	text          string
	start, end    int
}

// buildParagraphsMatchingDocument creates new paragraphs from existing
// exemplars whenever possible. The model only has to identify a semantic role
// (body/Heading1-3); direct formatting, language, spacing and indentation are
// inherited from the document instead of being guessed in the prompt.
func buildParagraphsMatchingDocument(doc []byte, text, style string) ([]byte, int) {
	templates := collectDocxParagraphTemplates(doc)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out bytes.Buffer
	for _, line := range lines {
		body, wantedStyle, forcePlain := docxLineIntent(line, style)
		if !forcePlain {
			if template := findDocxParagraphTemplate(templates, wantedStyle); template != nil {
				out.Write(rebuildParagraph(template.frag, template.openTag, body))
				continue
			}
		}
		fallback, _ := buildParagraphsXML(line, style)
		out.Write(fallback)
	}
	return out.Bytes(), len(lines)
}

func collectDocxParagraphTemplates(doc []byte) []docxParagraphTemplate {
	var templates []docxParagraphTemplate
	pos := 0
	for {
		open, openEnd, selfClosing := findNextParagraph(doc, pos)
		if open < 0 {
			break
		}
		if selfClosing {
			pos = openEnd
			continue
		}
		closeIdx := bytes.Index(doc[openEnd:], []byte("</w:p>"))
		if closeIdx < 0 {
			break
		}
		end := openEnd + closeIdx + len("</w:p>")
		frag := doc[open:end]
		if !docxOffsetInsideTable(doc, open) {
			visible, err := docxParagraphText(frag)
			if err == nil {
				templates = append(templates, docxParagraphTemplate{
					frag: frag, openTag: doc[open:openEnd],
					style: docxParagraphStyle(frag), text: visible,
					start: open, end: end,
				})
			}
		}
		pos = end
	}
	return templates
}

var docxParagraphSelectorRe = regexp.MustCompile(`(?i)^(?:/body/)?p\[([1-9][0-9]*)\]$`)

func parseDocxParagraphSelector(selector string, count int) (int, error) {
	selector = strings.TrimSpace(selector)
	m := docxParagraphSelectorRe.FindStringSubmatch(selector)
	if m == nil {
		return 0, fmt.Errorf("invalid selector %q (want /body/p[N])", selector)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 || n > count {
		return 0, fmt.Errorf("selector %q is outside 1..%d", selector, count)
	}
	return n - 1, nil
}

func docxOffsetInsideTable(doc []byte, offset int) bool {
	before := doc[:offset]
	return bytes.LastIndex(before, []byte("<w:tbl")) > bytes.LastIndex(before, []byte("</w:tbl>"))
}

func docxParagraphStyle(frag []byte) string {
	pPr := extractBlock(frag, "w:pPr")
	i := bytes.Index(pPr, []byte("<w:pStyle"))
	if i < 0 {
		return ""
	}
	gt := bytes.IndexByte(pPr[i:], '>')
	if gt < 0 {
		return ""
	}
	tag := pPr[i : i+gt+1]
	for _, attr := range [][]byte{[]byte(`w:val="`), []byte(`w:val='`)} {
		at := bytes.Index(tag, attr)
		if at < 0 {
			continue
		}
		start := at + len(attr)
		quote := attr[len(attr)-1]
		end := bytes.IndexByte(tag[start:], quote)
		if end >= 0 {
			return string(tag[start : start+end])
		}
	}
	return ""
}

// docxParagraphFormatSummary reports only formatting written directly on the
// paragraph/first run. The named style remains the source of truth for inherited
// formatting; these hints help the model choose a good exemplar without making
// it reconstruct WordprocessingML itself.
func docxParagraphFormatSummary(frag []byte) string {
	pPr := extractBlock(frag, "w:pPr")
	rest := frag
	if len(pPr) > 0 {
		if i := bytes.Index(rest, pPr); i >= 0 {
			rest = rest[i+len(pPr):]
		}
	}
	rPr := extractBlock(rest, "w:rPr")
	var hints []string
	if v := docxElementAttr(pPr, "w:jc", "w:val"); v != "" {
		hints = append(hints, "align="+v)
	}
	if v := docxElementAttr(rPr, "w:rFonts", "w:ascii"); v != "" {
		hints = append(hints, "font="+v)
	} else if v := docxElementAttr(rPr, "w:rFonts", "w:hAnsi"); v != "" {
		hints = append(hints, "font="+v)
	}
	if v := docxElementAttr(rPr, "w:sz", "w:val"); v != "" {
		if halfPoints, err := strconv.Atoi(v); err == nil {
			hints = append(hints, fmt.Sprintf("size=%gpt", float64(halfPoints)/2))
		} else {
			hints = append(hints, "size="+v)
		}
	}
	if docxToggleEnabled(rPr, "w:b") {
		hints = append(hints, "bold")
	}
	if docxToggleEnabled(rPr, "w:i") {
		hints = append(hints, "italic")
	}
	if v := docxElementAttr(rPr, "w:color", "w:val"); v != "" && !strings.EqualFold(v, "auto") {
		hints = append(hints, "color=#"+strings.TrimPrefix(v, "#"))
	}
	if v := docxElementAttr(rPr, "w:lang", "w:val"); v != "" {
		hints = append(hints, "lang="+v)
	}
	if v := docxElementAttr(pPr, "w:spacing", "w:after"); v != "" {
		hints = append(hints, "after="+v)
	}
	if v := docxElementAttr(pPr, "w:spacing", "w:line"); v != "" {
		hints = append(hints, "line="+v)
	}
	if len(hints) == 0 {
		return ""
	}
	return " " + strings.Join(hints, " ")
}

func docxElementAttr(block []byte, element, attr string) string {
	if len(block) == 0 {
		return ""
	}
	i := docxElementStart(block, element)
	if i < 0 {
		return ""
	}
	gt := bytes.IndexByte(block[i:], '>')
	if gt < 0 {
		return ""
	}
	tag := block[i : i+gt+1]
	for _, quote := range []byte{'"', '\''} {
		needle := []byte(attr + "=" + string(quote))
		at := bytes.Index(tag, needle)
		if at < 0 {
			continue
		}
		start := at + len(needle)
		end := bytes.IndexByte(tag[start:], quote)
		if end >= 0 {
			return string(tag[start : start+end])
		}
	}
	return ""
}

func docxElementStart(block []byte, element string) int {
	needle := []byte("<" + element)
	from := 0
	for from < len(block) {
		i := bytes.Index(block[from:], needle)
		if i < 0 {
			return -1
		}
		i += from
		after := i + len(needle)
		if after >= len(block) || block[after] == ' ' || block[after] == '>' || block[after] == '/' || block[after] == '\t' || block[after] == '\r' || block[after] == '\n' {
			return i
		}
		from = after
	}
	return -1
}

func docxToggleEnabled(block []byte, element string) bool {
	if len(block) == 0 {
		return false
	}
	i := docxElementStart(block, element)
	if i < 0 {
		return false
	}
	gt := bytes.IndexByte(block[i:], '>')
	if gt < 0 {
		return false
	}
	tag := block[i : i+gt+1]
	value := docxElementAttr(tag, element, "w:val")
	return value == "" || (value != "0" && !strings.EqualFold(value, "false") && !strings.EqualFold(value, "off"))
}

func docxLineIntent(line, forcedStyle string) (body, wantedStyle string, forcePlain bool) {
	body = line
	switch {
	case strings.HasPrefix(line, "### "):
		return line[4:], "Heading3", false
	case strings.HasPrefix(line, "## "):
		return line[3:], "Heading2", false
	case strings.HasPrefix(line, "# "):
		return line[2:], "Heading1", false
	}
	switch strings.ToLower(strings.TrimSpace(forcedStyle)) {
	case "heading1":
		wantedStyle = "Heading1"
	case "heading2":
		wantedStyle = "Heading2"
	case "heading3":
		wantedStyle = "Heading3"
	case "bold":
		return body, "", true
	}
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, "**") && strings.HasSuffix(trimmed, "**") && len(trimmed) > 4 {
		return body, wantedStyle, true
	}
	return body, wantedStyle, false
}

func findDocxParagraphTemplate(templates []docxParagraphTemplate, wantedStyle string) *docxParagraphTemplate {
	for i := len(templates) - 1; i >= 0; i-- {
		t := &templates[i]
		if strings.TrimSpace(t.text) == "" {
			continue
		}
		if wantedStyle != "" {
			if strings.EqualFold(t.style, wantedStyle) {
				return t
			}
			continue
		}
		if t.style == "" || strings.EqualFold(t.style, "Normal") || strings.EqualFold(t.style, "BodyText") {
			return t
		}
	}
	return nil
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
	if err := writeMinimalDocxTo(f, paraXML); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// WriteSimpleDocx writes a complete Word document to w from Markdown-ish
// text. It is shared by the edit_docx tool and lightweight UI exports, so a
// downloaded .docx is a real OOXML package rather than HTML with a .doc name.
func WriteSimpleDocx(w io.Writer, text string) error {
	paraXML, _ := buildParagraphsXML(text, "")
	return writeMinimalDocxTo(w, paraXML)
}

func writeMinimalDocxTo(dst io.Writer, paraXML []byte) error {
	zw := zip.NewWriter(dst)
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
	return nil
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
