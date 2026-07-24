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
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
