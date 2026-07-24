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
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

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
		rebuilt, replaced, err := replaceParagraphTextPreservingRuns(frag, find, repl)
		if err != nil {
			return nil, 0, err
		}
		if replaced != n {
			return nil, 0, fmt.Errorf("replace paragraph: internal match count %d != %d", replaced, n)
		}
		out.Write(doc[pos:open])
		out.Write(rebuilt)
		pos = paraEnd
	}
	return out.Bytes(), total, nil
}

type docxInlinePart struct {
	start, end               int
	logicalStart, logicalEnd int
	value                    string
}

type docxTextMatch struct {
	start, end int
}

// replaceParagraphTextPreservingRuns applies replacements from the end of the
// logical paragraph towards the beginning. Only inline <w:t>, <w:br> and
// <w:tab> spans are regenerated, so all surrounding <w:rPr>/<w:pPr> data stays
// exactly where Word put it. Replacement text is placed in the first affected
// part and therefore inherits that run's formatting.
func replaceParagraphTextPreservingRuns(frag []byte, find, repl string) ([]byte, int, error) {
	parts, logical, err := scanDocxInlineParts(frag)
	if err != nil {
		return nil, 0, err
	}
	var matches []docxTextMatch
	for from := 0; from <= len(logical)-len(find); {
		i := strings.Index(logical[from:], find)
		if i < 0 {
			break
		}
		start := from + i
		matches = append(matches, docxTextMatch{start: start, end: start + len(find)})
		from = start + len(find)
	}
	if len(matches) == 0 {
		return frag, 0, nil
	}

	values := make([]string, len(parts))
	changed := make([]bool, len(parts))
	for i := range parts {
		values[i] = parts[i].value
	}
	for mi := len(matches) - 1; mi >= 0; mi-- {
		m := matches[mi]
		first := inlinePartAt(parts, m.start)
		last := inlinePartAt(parts, m.end-1)
		if first < 0 || last < first {
			return nil, 0, fmt.Errorf("replace paragraph: cannot map logical range %d:%d", m.start, m.end)
		}
		firstOffset := m.start - parts[first].logicalStart
		lastOffset := m.end - parts[last].logicalStart
		if first == last {
			values[first] = values[first][:firstOffset] + repl + values[first][lastOffset:]
			changed[first] = true
			continue
		}
		values[first] = values[first][:firstOffset] + repl
		changed[first] = true
		for i := first + 1; i < last; i++ {
			values[i] = ""
			changed[i] = true
		}
		values[last] = values[last][lastOffset:]
		changed[last] = true
	}

	var out bytes.Buffer
	pos := 0
	for i, part := range parts {
		out.Write(frag[pos:part.start])
		if changed[i] {
			writeDocxInlineText(&out, values[i])
		} else {
			out.Write(frag[part.start:part.end])
		}
		pos = part.end
	}
	out.Write(frag[pos:])
	return out.Bytes(), len(matches), nil
}

func inlinePartAt(parts []docxInlinePart, logicalOffset int) int {
	for i, part := range parts {
		if logicalOffset >= part.logicalStart && logicalOffset < part.logicalEnd {
			return i
		}
	}
	return -1
}

func scanDocxInlineParts(frag []byte) ([]docxInlinePart, string, error) {
	var parts []docxInlinePart
	var logical strings.Builder
	pos := 0
	for pos < len(frag) {
		start, tag := nextDocxInlineTag(frag, pos)
		if start < 0 {
			break
		}
		gt := bytes.IndexByte(frag[start:], '>')
		if gt < 0 {
			return nil, "", fmt.Errorf("parse paragraph: unclosed <%s>", tag)
		}
		openEnd := start + gt + 1
		end := openEnd
		value := ""
		switch tag {
		case "w:t":
			if frag[openEnd-2] == '/' {
				parts = append(parts, docxInlinePart{
					start: start, end: openEnd,
					logicalStart: logical.Len(), logicalEnd: logical.Len(),
				})
				pos = openEnd
				continue
			}
			closeTag := []byte("</w:t>")
			closeAt := bytes.Index(frag[openEnd:], closeTag)
			if closeAt < 0 {
				return nil, "", fmt.Errorf("parse paragraph: unclosed <w:t>")
			}
			contentEnd := openEnd + closeAt
			end = contentEnd + len(closeTag)
			decoded, err := decodeDocxText(frag[openEnd:contentEnd])
			if err != nil {
				return nil, "", err
			}
			value = decoded
		case "w:br":
			value = "\n"
			if frag[openEnd-2] != '/' {
				if closeAt := bytes.Index(frag[openEnd:], []byte("</w:br>")); closeAt >= 0 {
					end = openEnd + closeAt + len("</w:br>")
				}
			}
		case "w:tab":
			value = "\t"
			if frag[openEnd-2] != '/' {
				if closeAt := bytes.Index(frag[openEnd:], []byte("</w:tab>")); closeAt >= 0 {
					end = openEnd + closeAt + len("</w:tab>")
				}
			}
		}
		logicalStart := logical.Len()
		logical.WriteString(value)
		parts = append(parts, docxInlinePart{
			start: start, end: end, value: value,
			logicalStart: logicalStart, logicalEnd: logical.Len(),
		})
		pos = end
	}
	return parts, logical.String(), nil
}

func nextDocxInlineTag(frag []byte, pos int) (int, string) {
	best := -1
	bestTag := ""
	for _, tag := range []string{"w:t", "w:br", "w:tab"} {
		needle := []byte("<" + tag)
		search := pos
		for search < len(frag) {
			i := bytes.Index(frag[search:], needle)
			if i < 0 {
				break
			}
			start := search + i
			after := start + len(needle)
			if after < len(frag) && (frag[after] == '>' || frag[after] == ' ' || frag[after] == '/') {
				if best < 0 || start < best {
					best, bestTag = start, tag
				}
				break
			}
			search = after
		}
	}
	return best, bestTag
}

func decodeDocxText(raw []byte) (string, error) {
	wrapped := append([]byte("<root>"), raw...)
	wrapped = append(wrapped, []byte("</root>")...)
	var value struct {
		Text string `xml:",chardata"`
	}
	if err := xml.Unmarshal(wrapped, &value); err != nil {
		return "", fmt.Errorf("parse paragraph text: %w", err)
	}
	return value.Text, nil
}

func writeDocxInlineText(out *bytes.Buffer, text string) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i > 0 {
			out.WriteString("<w:br/>")
		}
		writeRunText(out, line)
	}
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
