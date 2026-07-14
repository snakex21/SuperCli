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
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"supercli/internal/tools/fileops"
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
			"across formatting runs; optional header/footer inclusion), 'append' (add paragraphs at the end, optional style: Heading1-3 or bold; " +
			"by default new paragraphs match an existing paragraph of the same role), " +
			"'clone' (copy a paragraph selected as /body/p[N], optionally replace its text, and insert it after " +
			"another paragraph while preserving its formatting), " +
			"'replace_at' (replace exactly one selected paragraph while preserving its style), " +
			"'suggest_at' (the same precise edit as a minimal Word tracked change), " +
			"'comment_at' (attach a review comment to one selected paragraph without changing its text), " +
			"'create' (new .docx from text; lines starting with '# ', '## ', '### ' become headings). " +
			"Safety: before an existing file is changed, a backup copy is saved next to it as '<name>.bak', " +
			"and the write is atomic — tell the user the backup exists if they want to undo. " +
			"Everything not edited (images, styles, headers, tables) is preserved byte-for-byte. Mixed " +
			"per-word formatting is retained; replacement text inherits the first affected run's formatting. " +
			"Set style_mode='plain' only when document-style matching is not wanted. " +
			"Do NOT use this for spreadsheets (use edit_xlsx), " +
			"PDFs (cannot be edited), or plain text files (use the line-edit tools). " +
			"Set dry_run=true to validate and preview an edit without writing or creating a backup. " +
			"Do NOT use 'create' to overwrite an existing file the user did not ask to replace.",
		Schema: `{
  "type": "object",
  "properties": {
    "path":    {"type": "string", "description": "Path to the .docx file (relative paths resolve against the working directory)."},
    "action":  {"type": "string", "enum": ["replace", "replace_at", "suggest_at", "comment_at", "append", "clone", "create"], "description": "What to do."},
    "find":    {"type": "string", "description": "replace: the exact text to find (case-sensitive)."},
    "replace": {"type": "string", "description": "replace: the replacement text (may be empty to delete)."},
    "text":    {"type": "string", "description": "replace_at/clone/append/create: replacement or new content. Append/create use one paragraph per line; '# ', '## ', '### ' prefixes become headings."},
    "style":   {"type": "string", "description": "append: optional semantic style for all appended paragraphs: 'Heading1', 'Heading2', 'Heading3', 'bold', or 'normal'."},
    "style_mode": {"type": "string", "enum": ["match", "plain"], "description": "append: 'match' (default) clones paragraph/run formatting from a similar existing paragraph; 'plain' uses only the requested built-in style."},
    "source": {"type": "string", "description": "replace_at/suggest_at/comment_at/clone: selector from read_docx selectors=true."},
    "comment": {"type": "string", "description": "comment_at: review comment text."},
    "author": {"type": "string", "description": "suggest_at/comment_at: reviewer name (default SuperCli)."},
    "initials": {"type": "string", "description": "comment_at: reviewer initials (derived from author when omitted)."},
    "after": {"type": "string", "description": "clone: insert after /body/p[N]; defaults to source. Use 'end' to append before section properties."},
    "dry_run": {"type": "boolean", "description": "Validate and describe the change without writing the document."},
    "include_headers": {"type": "boolean", "description": "replace: also replace matching text in word/header*.xml."},
    "include_footers": {"type": "boolean", "description": "replace: also replace matching text in word/footer*.xml."}
  },
  "required": ["path", "action"]
}`,
		Fn: t.Execute,
	}
}

type editDocxArgs struct {
	Path           string `json:"path"`
	Action         string `json:"action"`
	Find           string `json:"find"`
	Replace        string `json:"replace"`
	Text           string `json:"text"`
	Style          string `json:"style"`
	StyleMode      string `json:"style_mode"`
	Source         string `json:"source"`
	After          string `json:"after"`
	DryRun         bool   `json:"dry_run"`
	IncludeHeaders bool   `json:"include_headers"`
	IncludeFooters bool   `json:"include_footers"`
	Comment        string `json:"comment"`
	Author         string `json:"author"`
	Initials       string `json:"initials"`
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
	release := fileops.LockMutationPaths(full)
	defer release()
	switch p.Action {
	case "replace":
		return t.doReplace(full, p)
	case "replace_at":
		return t.doReplaceAt(full, p)
	case "suggest_at":
		return t.doSuggestAt(full, p)
	case "comment_at":
		return t.doCommentAt(full, p)
	case "append":
		return t.doAppend(full, p)
	case "clone":
		return t.doClone(full, p)
	case "create":
		return t.doCreate(full, p)
	default:
		err := fmt.Errorf("edit_docx: unknown action %q (want replace|replace_at|suggest_at|comment_at|append|clone|create)", p.Action)
		return Result{Err: err}, err
	}
}

func (t *EditDocxTool) loadDocumentXML(full string) ([]byte, error) {
	info, err := os.Stat(full)
	if err != nil {
		return nil, fileops.FileErr(err, full)
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
	updates := map[string][]byte{}
	if count > 0 {
		updates[docxDocumentEntry] = newDoc
	}
	partsChanged := 0
	if p.IncludeHeaders || p.IncludeFooters {
		parts, partErr := listDocxStoryParts(full, p.IncludeHeaders, p.IncludeFooters, t.MaxDocxBytes)
		if partErr != nil {
			return Result{Err: fmt.Errorf("edit_docx: %w", partErr)}, partErr
		}
		for _, name := range parts {
			part, readErr := readZipEntry(full, name, t.MaxDocxBytes)
			if readErr != nil {
				return Result{Err: fmt.Errorf("edit_docx: %s: %w", name, readErr)}, readErr
			}
			changed, n, replaceErr := docxReplaceAll(part, p.Find, p.Replace)
			if replaceErr != nil {
				return Result{Err: fmt.Errorf("edit_docx: %s: %w", name, replaceErr)}, replaceErr
			}
			if n > 0 {
				updates[name] = changed
				count += n
				partsChanged++
			}
		}
	}
	if count == 0 {
		return Result{Text: fmt.Sprintf("No occurrences of %q found in %s. Nothing was changed (no backup created).", p.Find, full)}, nil
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would replace %d occurrence(s) of %q in %s across %d additional header/footer part(s). Nothing was written.", count, p.Find, full, partsChanged)}, nil
	}
	backup, err := editZipEntriesInPlace(full, updates)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Replaced %d occurrence(s) of %q in %s across %d additional header/footer part(s). Backup of the original saved as %s.", count, p.Find, full, partsChanged, backup)}, nil
}

func (t *EditDocxTool) doReplaceAt(full string, p editDocxArgs) (Result, error) {
	if strings.TrimSpace(p.Source) == "" {
		err := fmt.Errorf("edit_docx replace_at: 'source' is required (for example /body/p[3])")
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	source, err := findDocxParagraphLocation(doc, p.Source)
	if err != nil {
		err = fmt.Errorf("edit_docx replace_at source: %w", err)
		return Result{Err: err}, err
	}
	if source.text == p.Text {
		return Result{Text: fmt.Sprintf("%s already contains the requested text in %s. Nothing was changed.", p.Source, full)}, nil
	}
	var replacement []byte
	if source.text == "" {
		replacement = rebuildParagraph(source.frag, source.openTag, p.Text)
	} else {
		replacement, _, err = replaceParagraphTextPreservingRuns(source.frag, source.text, p.Text)
		if err != nil {
			return Result{Err: fmt.Errorf("edit_docx replace_at: %w", err)}, err
		}
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would replace %s text %q with %q in %s while preserving its formatting. Nothing was written.", p.Source, source.text, p.Text, full)}, nil
	}
	newDoc := spliceBytes(doc, source.start, source.end, replacement)
	backup, err := editZipEntryInPlace(full, docxDocumentEntry, newDoc)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Replaced %s text in %s while preserving its formatting. Backup of the original saved as %s.", p.Source, full, backup)}, nil
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
	styleMode := strings.ToLower(strings.TrimSpace(p.StyleMode))
	if styleMode == "" {
		styleMode = "match"
	}
	if styleMode != "match" && styleMode != "plain" {
		err := fmt.Errorf("edit_docx append: bad style_mode %q (want match|plain)", p.StyleMode)
		return Result{Err: err}, err
	}
	var paraXML []byte
	var nParas int
	if styleMode == "match" {
		paraXML, nParas = buildParagraphsMatchingDocument(doc, p.Text, p.Style)
	} else {
		paraXML, nParas = buildParagraphsXML(p.Text, p.Style)
	}
	newDoc, err := docxInsertBeforeBodyEnd(doc, paraXML)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would append %d paragraph(s) to %s using style_mode=%s. Nothing was written.", nParas, full, styleMode)}, nil
	}
	backup, err := editZipEntryInPlace(full, docxDocumentEntry, newDoc)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Appended %d paragraph(s) to %s. Backup of the original saved as %s.", nParas, full, backup)}, nil
}

func (t *EditDocxTool) doClone(full string, p editDocxArgs) (Result, error) {
	if strings.TrimSpace(p.Source) == "" {
		err := fmt.Errorf("edit_docx clone: 'source' is required (for example /body/p[3])")
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	paragraphs := collectDocxParagraphTemplates(doc)
	sourceIndex, err := parseDocxParagraphSelector(p.Source, len(paragraphs))
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx clone source: %w", err)}, err
	}
	source := paragraphs[sourceIndex]
	cloned := append([]byte(nil), source.frag...)
	if p.Text != "" {
		if source.text == "" {
			cloned = rebuildParagraph(source.frag, source.openTag, p.Text)
		} else {
			cloned, _, err = replaceParagraphTextPreservingRuns(source.frag, source.text, p.Text)
			if err != nil {
				return Result{Err: fmt.Errorf("edit_docx clone: replace cloned text: %w", err)}, err
			}
		}
	}

	var newDoc []byte
	after := strings.TrimSpace(p.After)
	if strings.EqualFold(after, "end") {
		newDoc, err = docxInsertBeforeBodyEnd(doc, cloned)
	} else {
		if after == "" {
			after = p.Source
		}
		afterIndex, selectorErr := parseDocxParagraphSelector(after, len(paragraphs))
		if selectorErr != nil {
			err = fmt.Errorf("edit_docx clone after: %w", selectorErr)
			return Result{Err: err}, err
		}
		insertAt := paragraphs[afterIndex].end
		newDoc = spliceBytes(doc, insertAt, insertAt, cloned)
	}
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx clone: %w", err)}, err
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would clone %s in %s after %s while preserving its formatting. Nothing was written.", p.Source, full, after)}, nil
	}
	backup, err := editZipEntryInPlace(full, docxDocumentEntry, newDoc)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Cloned %s in %s after %s. Backup of the original saved as %s.", p.Source, full, after, backup)}, nil
}

func (t *EditDocxTool) doCreate(full string, p editDocxArgs) (Result, error) {
	if _, err := os.Lstat(full); err == nil {
		err := fmt.Errorf("edit_docx create: %q already exists. Refusing to overwrite — ask the user whether to replace it, pick a different name, or use action 'append'/'replace' to modify it", full)
		return Result{Err: err}, err
	}
	if p.DryRun {
		_, nParas := buildParagraphsXML(p.Text, "")
		return Result{Text: fmt.Sprintf("Preview only: would create %s with %d paragraph(s). Nothing was written.", full, nParas)}, nil
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
// run text contains find, and rewrites only the affected inline
// text nodes. Paragraph/run properties and unrelated XML remain
// untouched. Returns the new document bytes and occurrence count.
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
