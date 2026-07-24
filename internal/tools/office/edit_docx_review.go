package office

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"time"
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
