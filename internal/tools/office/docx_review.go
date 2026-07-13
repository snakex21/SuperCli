package office

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	docxSettingsEntry = "word/settings.xml"
	docxCommentsEntry = "word/comments.xml"
	docxRelsEntry     = "word/_rels/document.xml.rels"
	docxTypesEntry    = "[Content_Types].xml"
	relNS             = "http://schemas.openxmlformats.org/package/2006/relationships"
)

var (
	wIDPattern  = regexp.MustCompile(`\bw:id="([0-9]+)"`)
	rIDPattern  = regexp.MustCompile(`\bId="rId([0-9]+)"`)
	proofState  = regexp.MustCompile(`(?s)<w:proofState\b[^>]*/>`)
	unsafeTrack = regexp.MustCompile(`<w:(?:hyperlink|drawing|object|fldChar|bookmark|ins\b|del\b|commentRange|sdt\b)`)
)

type docxStyledRun struct {
	start, end int
	openTag    []byte
	rPr        []byte
	text       string
}

func (t *EditDocxTool) doSuggestAt(full string, p editDocxArgs) (Result, error) {
	if strings.TrimSpace(p.Source) == "" {
		err := fmt.Errorf("edit_docx suggest_at: 'source' is required")
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	loc, err := findDocxParagraphLocation(doc, p.Source)
	if err != nil {
		err = fmt.Errorf("edit_docx suggest_at source: %w", err)
		return Result{Err: err}, err
	}
	if loc.text == p.Text {
		return Result{Text: fmt.Sprintf("%s already contains the requested text in %s. Nothing was changed.", p.Source, full)}, nil
	}
	author := strings.TrimSpace(p.Author)
	if author == "" {
		author = "SuperCli"
	}
	tracked, err := buildTrackedParagraph(loc.frag, loc.openTag, loc.text, p.Text, author, nextWordID(doc))
	if err != nil {
		err = fmt.Errorf("edit_docx suggest_at: %w", err)
		return Result{Err: err}, err
	}
	newDoc := spliceBytes(doc, loc.start, loc.end, tracked)
	settings, _, err := readOptionalZipEntry(full, docxSettingsEntry, t.MaxDocxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	settings = ensureTrackRevisions(settings)
	rels, _, err := readOptionalZipEntry(full, docxRelsEntry, t.MaxDocxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	rels = ensureDocxRelationship(rels, "http://schemas.openxmlformats.org/officeDocument/2006/relationships/settings", "settings.xml")
	types, _, err := readOptionalZipEntry(full, docxTypesEntry, t.MaxDocxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	types = ensureDocxContentType(types, "/word/settings.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml")
	updates := map[string][]byte{docxDocumentEntry: newDoc, docxSettingsEntry: settings, docxRelsEntry: rels, docxTypesEntry: types}
	if err := validateXMLUpdates(updates); err != nil {
		return Result{Err: fmt.Errorf("edit_docx suggest_at: %w", err)}, err
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would suggest replacing %q with %q at %s in %s as %s. Nothing was written.", loc.text, p.Text, p.Source, full, author)}, nil
	}
	backup, err := editZipEntriesInPlace(full, updates)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Added a minimal tracked replacement at %s in %s as %s. Backup saved as %s.", p.Source, full, author, backup)}, nil
}

func (t *EditDocxTool) doCommentAt(full string, p editDocxArgs) (Result, error) {
	if strings.TrimSpace(p.Source) == "" || strings.TrimSpace(p.Comment) == "" {
		err := fmt.Errorf("edit_docx comment_at: 'source' and non-empty 'comment' are required")
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	loc, err := findDocxParagraphLocation(doc, p.Source)
	if err != nil {
		err = fmt.Errorf("edit_docx comment_at source: %w", err)
		return Result{Err: err}, err
	}
	comments, exists, err := readOptionalZipEntry(full, docxCommentsEntry, t.MaxDocxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	id := nextWordID(comments)
	author := strings.TrimSpace(p.Author)
	if author == "" {
		author = "SuperCli"
	}
	initials := strings.TrimSpace(p.Initials)
	if initials == "" {
		initials = authorInitials(author)
	}
	newParagraph, err := addParagraphCommentMarkers(loc.frag, id)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx comment_at: %w", err)}, err
	}
	newDoc := spliceBytes(doc, loc.start, loc.end, newParagraph)
	comments = appendDocxComment(comments, exists, id, author, initials, p.Comment)
	rels, _, err := readOptionalZipEntry(full, docxRelsEntry, t.MaxDocxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	rels = ensureDocxRelationship(rels, "http://schemas.openxmlformats.org/officeDocument/2006/relationships/comments", "comments.xml")
	types, _, err := readOptionalZipEntry(full, docxTypesEntry, t.MaxDocxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	types = ensureDocxContentType(types, "/word/comments.xml", "application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml")
	updates := map[string][]byte{docxDocumentEntry: newDoc, docxCommentsEntry: comments, docxRelsEntry: rels, docxTypesEntry: types}
	if err := validateXMLUpdates(updates); err != nil {
		return Result{Err: fmt.Errorf("edit_docx comment_at: %w", err)}, err
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would add comment %q at %s in %s as %s. Nothing was written.", p.Comment, p.Source, full, author)}, nil
	}
	backup, err := editZipEntriesInPlace(full, updates)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Added comment %d at %s in %s as %s. Backup saved as %s.", id, p.Source, full, author, backup)}, nil
}

func buildTrackedParagraph(frag, openTag []byte, oldText, newText, author string, id int) ([]byte, error) {
	if unsafeTrack.Match(frag) || bytes.Contains(frag, []byte("<w:br")) || bytes.Contains(frag, []byte("<w:tab")) {
		return nil, fmt.Errorf("selected paragraph contains a field, link, drawing, existing review markup, line break or tab; use a simpler selector or ordinary replace_at")
	}
	runs, logical, err := collectStyledRuns(frag)
	if err != nil {
		return nil, err
	}
	if logical != oldText || len(runs) == 0 {
		return nil, fmt.Errorf("selected paragraph is not a simple styled-text paragraph")
	}
	prefix, suffix := commonUTF8Edges(oldText, newText)
	oldEnd := len(oldText) - suffix
	newEnd := len(newText) - suffix
	date := time.Now().UTC().Format(time.RFC3339)
	rsid := newRSID()
	var out bytes.Buffer
	out.Write(openTag)
	pPr := extractBlock(frag, "w:pPr")
	out.Write(pPr)
	writeStyledRange(&out, runs, logical, 0, prefix, false, "")
	if prefix < oldEnd {
		fmt.Fprintf(&out, `<w:del w:id="%d" w:author="%s" w:date="%s">`, id, xmlEscapeText(author), date)
		writeStyledRange(&out, runs, logical, prefix, oldEnd, true, rsid)
		out.WriteString(`</w:del>`)
		id++
	}
	if prefix < newEnd {
		style := styleAt(runs, prefix)
		fmt.Fprintf(&out, `<w:ins w:id="%d" w:author="%s" w:date="%s"><w:r w:rsidR="%s">`, id, xmlEscapeText(author), date, rsid)
		out.Write(style)
		writeReviewText(&out, "w:t", newText[prefix:newEnd])
		out.WriteString(`</w:r></w:ins>`)
	}
	writeStyledRange(&out, runs, logical, oldEnd, len(logical), false, "")
	out.WriteString(`</w:p>`)
	return out.Bytes(), nil
}

func collectStyledRuns(frag []byte) ([]docxStyledRun, string, error) {
	var runs []docxStyledRun
	var logical strings.Builder
	pos := bytes.IndexByte(frag, '>') + 1
	for pos > 0 && pos < len(frag) {
		start := findExactTag(frag, pos, "w:r")
		if start < 0 {
			break
		}
		gt := bytes.IndexByte(frag[start:], '>')
		if gt < 0 {
			return nil, "", fmt.Errorf("unclosed w:r")
		}
		openEnd := start + gt + 1
		closeAt := bytes.Index(frag[openEnd:], []byte("</w:r>"))
		if closeAt < 0 {
			return nil, "", fmt.Errorf("unclosed w:r")
		}
		end := openEnd + closeAt + len("</w:r>")
		part := frag[start:end]
		text, err := docxParagraphText(part)
		if err != nil {
			return nil, "", err
		}
		if text != "" {
			runs = append(runs, docxStyledRun{start: logical.Len(), end: logical.Len() + len(text), openTag: append([]byte(nil), frag[start:openEnd]...), rPr: append([]byte(nil), extractBlock(part, "w:rPr")...), text: text})
			logical.WriteString(text)
		}
		pos = end
	}
	return runs, logical.String(), nil
}

func findExactTag(data []byte, pos int, tag string) int {
	needle := []byte("<" + tag)
	for pos < len(data) {
		i := bytes.Index(data[pos:], needle)
		if i < 0 {
			return -1
		}
		at := pos + i
		after := at + len(needle)
		if after < len(data) && (data[after] == '>' || data[after] == ' ' || data[after] == '/') {
			return at
		}
		pos = after
	}
	return -1
}

func commonUTF8Edges(a, b string) (int, int) {
	prefix := 0
	for prefix < len(a) && prefix < len(b) {
		ra, sa := utf8.DecodeRuneInString(a[prefix:])
		rb, sb := utf8.DecodeRuneInString(b[prefix:])
		if ra != rb || sa != sb {
			break
		}
		prefix += sa
	}
	suffix := 0
	for len(a)-suffix > prefix && len(b)-suffix > prefix {
		ra, sa := utf8.DecodeLastRuneInString(a[:len(a)-suffix])
		rb, sb := utf8.DecodeLastRuneInString(b[:len(b)-suffix])
		if ra != rb || sa != sb {
			break
		}
		suffix += sa
	}
	return prefix, suffix
}

func writeStyledRange(out *bytes.Buffer, runs []docxStyledRun, logical string, from, to int, deleted bool, rsid string) {
	for _, run := range runs {
		start, end := maxInt(from, run.start), minInt(to, run.end)
		if start >= end {
			continue
		}
		if deleted {
			fmt.Fprintf(out, `<w:r w:rsidDel="%s">`, rsid)
		} else {
			out.Write(run.openTag)
		}
		out.Write(run.rPr)
		tag := "w:t"
		if deleted {
			tag = "w:delText"
		}
		writeReviewText(out, tag, logical[start:end])
		out.WriteString(`</w:r>`)
	}
}

func styleAt(runs []docxStyledRun, offset int) []byte {
	for _, run := range runs {
		if offset >= run.start && offset < run.end {
			return run.rPr
		}
	}
	if len(runs) > 0 {
		return runs[len(runs)-1].rPr
	}
	return nil
}

func writeReviewText(out *bytes.Buffer, tag, value string) {
	space := ""
	if value != strings.TrimSpace(value) {
		space = ` xml:space="preserve"`
	}
	fmt.Fprintf(out, "<%s%s>%s</%s>", tag, space, xmlEscapeText(value), tag)
}

func addParagraphCommentMarkers(frag []byte, id int) ([]byte, error) {
	closeAt := bytes.LastIndex(frag, []byte("</w:p>"))
	if closeAt < 0 {
		return nil, fmt.Errorf("malformed selected paragraph")
	}
	insertAt := bytes.IndexByte(frag, '>') + 1
	if pPr := extractBlock(frag, "w:pPr"); len(pPr) > 0 {
		if at := bytes.Index(frag, pPr); at >= 0 {
			insertAt = at + len(pPr)
		}
	}
	var out bytes.Buffer
	out.Write(frag[:insertAt])
	fmt.Fprintf(&out, `<w:commentRangeStart w:id="%d"/>`, id)
	out.Write(frag[insertAt:closeAt])
	fmt.Fprintf(&out, `<w:commentRangeEnd w:id="%d"/><w:r><w:rPr><w:rStyle w:val="CommentReference"/></w:rPr><w:commentReference w:id="%d"/></w:r>`, id, id)
	out.Write(frag[closeAt:])
	return out.Bytes(), nil
}

func appendDocxComment(data []byte, exists bool, id int, author, initials, comment string) []byte {
	if !exists || len(bytes.TrimSpace(data)) == 0 {
		data = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:comments xmlns:w="` + wordProcessingNS + `"></w:comments>`)
	}
	closeAt := bytes.LastIndex(data, []byte("</w:comments>"))
	if closeAt < 0 {
		return data
	}
	date := time.Now().UTC().Format(time.RFC3339)
	var item bytes.Buffer
	fmt.Fprintf(&item, `<w:comment w:id="%d" w:author="%s" w:initials="%s" w:date="%s"><w:p><w:r>`, id, xmlEscapeText(author), xmlEscapeText(initials), date)
	writeReviewText(&item, "w:t", comment)
	item.WriteString(`</w:r></w:p></w:comment>`)
	return spliceBytes(data, closeAt, closeAt, item.Bytes())
}

func readOptionalZipEntry(path, name string, maxBytes int64) ([]byte, bool, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, false, fmt.Errorf("open docx package: %w", err)
	}
	defer zr.Close()
	for _, file := range zr.File {
		if file.Name != name {
			continue
		}
		if int64(file.UncompressedSize64) > maxBytes {
			return nil, false, fmt.Errorf("docx part %s is too large", name)
		}
		rc, err := file.Open()
		if err != nil {
			return nil, false, err
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxBytes+1))
		if err != nil {
			return nil, false, err
		}
		if int64(len(data)) > maxBytes {
			return nil, false, fmt.Errorf("docx part %s is too large", name)
		}
		return data, true, nil
	}
	return nil, false, nil
}

func ensureTrackRevisions(settings []byte) []byte {
	if len(settings) == 0 {
		return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:settings xmlns:w="` + wordProcessingNS + `"><w:trackRevisions/></w:settings>`)
	}
	if bytes.Contains(settings, []byte("<w:trackRevisions")) {
		return settings
	}
	if span := proofState.FindIndex(settings); span != nil {
		return spliceBytes(settings, span[1], span[1], []byte("<w:trackRevisions/>"))
	}
	gt := bytes.IndexByte(settings, '>')
	if bytes.HasPrefix(settings, []byte("<?xml")) {
		if endDecl := bytes.Index(settings, []byte("?>")); endDecl >= 0 {
			gt = endDecl + 2 + bytes.IndexByte(settings[endDecl+2:], '>')
		}
	}
	if gt < 0 {
		return settings
	}
	return spliceBytes(settings, gt+1, gt+1, []byte("<w:trackRevisions/>"))
}

func ensureDocxRelationship(data []byte, relType, target string) []byte {
	if len(data) == 0 {
		data = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="` + relNS + `"></Relationships>`)
	}
	if bytes.Contains(data, []byte(`Type="`+relType+`"`)) {
		return data
	}
	closeAt := bytes.LastIndex(data, []byte("</Relationships>"))
	if closeAt < 0 {
		return data
	}
	n := 1
	for _, match := range rIDPattern.FindAllSubmatch(data, -1) {
		value, _ := strconv.Atoi(string(match[1]))
		if value >= n {
			n = value + 1
		}
	}
	rel := fmt.Sprintf(`<Relationship Id="rId%d" Type="%s" Target="%s"/>`, n, relType, target)
	return spliceBytes(data, closeAt, closeAt, []byte(rel))
}

func ensureDocxContentType(data []byte, part, contentType string) []byte {
	if len(data) == 0 {
		data = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`)
	}
	if bytes.Contains(data, []byte(`PartName="`+part+`"`)) {
		return data
	}
	closeAt := bytes.LastIndex(data, []byte("</Types>"))
	if closeAt < 0 {
		return data
	}
	override := fmt.Sprintf(`<Override PartName="%s" ContentType="%s"/>`, part, contentType)
	return spliceBytes(data, closeAt, closeAt, []byte(override))
}

func nextWordID(data []byte) int {
	n := 0
	for _, match := range wIDPattern.FindAllSubmatch(data, -1) {
		value, _ := strconv.Atoi(string(match[1]))
		if value >= n {
			n = value + 1
		}
	}
	return n
}

func newRSID() string {
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "00C1A001"
	}
	return strings.ToUpper(hex.EncodeToString(raw[:]))
}

func authorInitials(author string) string {
	var out []rune
	for _, field := range strings.Fields(author) {
		for _, r := range field {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out = append(out, unicode.ToUpper(r))
				break
			}
		}
		if len(out) == 3 {
			break
		}
	}
	if len(out) == 0 {
		return "SC"
	}
	return string(out)
}

func validateXMLUpdates(updates map[string][]byte) error {
	for name, data := range updates {
		dec := xml.NewDecoder(bytes.NewReader(data))
		for {
			_, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("generated %s is not well-formed XML: %w", name, err)
			}
		}
	}
	return nil
}

func renderDocxComments(data []byte, remaining int64) (string, error) {
	if remaining <= 0 {
		return "", fmt.Errorf("rendered document exceeds output limit")
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "comment" {
			continue
		}
		id, author := "?", ""
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "id":
				id = attr.Value
			case "author":
				author = attr.Value
			}
		}
		var body strings.Builder
		for {
			inner, err := dec.Token()
			if err != nil {
				return "", err
			}
			switch node := inner.(type) {
			case xml.StartElement:
				if node.Name.Local == "t" {
					var value string
					if err := dec.DecodeElement(&value, &node); err != nil {
						return "", err
					}
					body.WriteString(value)
				}
			case xml.EndElement:
				if node.Name.Local == "comment" {
					if out.Len() == 0 {
						out.WriteString("\n== comments ==\n")
					}
					fmt.Fprintf(&out, "[id=%s author=%s] %s\n", id, author, body.String())
					goto nextComment
				}
			}
		}
	nextComment:
		if int64(out.Len()) > remaining {
			return "", fmt.Errorf("rendered comments exceed output limit")
		}
	}
	return out.String(), nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
