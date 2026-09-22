package office

import (
	"bytes"
	"fmt"
	"math"
	"regexp"
	"strings"
)

var (
	docxColorRe = regexp.MustCompile(`(?i)^[0-9a-f]{6}$`)
	docxStyleRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

func (t *EditDocxTool) doFormatAt(full string, p editDocxArgs) (Result, error) {
	if strings.TrimSpace(p.Source) == "" {
		err := fmt.Errorf("edit_docx format_at: 'source' is required")
		return Result{Err: err}, err
	}
	if p.Bold == nil && p.Italic == nil && p.Underline == nil && p.FontSizePT == 0 &&
		strings.TrimSpace(p.Color) == "" && strings.TrimSpace(p.Alignment) == "" && strings.TrimSpace(p.Style) == "" {
		err := fmt.Errorf("edit_docx format_at: provide at least one formatting field")
		return Result{Err: err}, err
	}
	if p.FontSizePT != 0 && (p.FontSizePT < 6 || p.FontSizePT > 96) {
		err := fmt.Errorf("edit_docx format_at: font_size_pt %.2f is outside 6..96", p.FontSizePT)
		return Result{Err: err}, err
	}
	color := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(p.Color), "#"))
	if color != "" && !docxColorRe.MatchString(color) {
		err := fmt.Errorf("edit_docx format_at: color %q must be six hexadecimal RGB digits", p.Color)
		return Result{Err: err}, err
	}
	alignment := strings.ToLower(strings.TrimSpace(p.Alignment))
	if alignment != "" && alignment != "left" && alignment != "center" && alignment != "right" && alignment != "justify" {
		err := fmt.Errorf("edit_docx format_at: alignment %q must be left|center|right|justify", p.Alignment)
		return Result{Err: err}, err
	}
	style := strings.TrimSpace(p.Style)
	if style != "" && !docxStyleRe.MatchString(style) {
		err := fmt.Errorf("edit_docx format_at: style %q is not a safe Word style id", style)
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	source, err := findDocxParagraphLocation(doc, p.Source)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx format_at: %w", err)}, err
	}
	replacement := append([]byte(nil), source.frag...)
	if style != "" || alignment != "" {
		replacement = formatDocxParagraphProperties(replacement, style, alignment)
	}
	if p.Bold != nil || p.Italic != nil || p.Underline != nil || p.FontSizePT != 0 || color != "" {
		replacement = formatDocxParagraphRuns(replacement, p.Bold, p.Italic, p.Underline, p.FontSizePT, color)
	}
	if bytes.Equal(replacement, source.frag) {
		return Result{Text: fmt.Sprintf("%s already has the requested formatting in %s. Nothing was changed.", p.Source, full)}, nil
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would format %s in %s without changing its text. Nothing was written.", p.Source, full)}, nil
	}
	newDoc := spliceBytes(doc, source.start, source.end, replacement)
	backup, err := editZipEntryInPlace(full, docxDocumentEntry, newDoc)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx format_at: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Formatted %s in %s without changing its text. Backup of the original saved as %s.", p.Source, full, backup)}, nil
}

func (t *EditDocxTool) doInsertPageBreak(full string, p editDocxArgs) (Result, error) {
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	pageBreak := []byte(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
	after := strings.TrimSpace(p.After)
	var newDoc []byte
	if after == "" || strings.EqualFold(after, "end") {
		newDoc, err = docxInsertBeforeBodyEnd(doc, pageBreak)
		after = "end"
	} else {
		var insertAt int
		insertAt, err = findDocxInsertionPoint(doc, after)
		if err == nil {
			newDoc = spliceBytes(doc, insertAt, insertAt, pageBreak)
		}
	}
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_page_break: %w", err)}, err
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would insert a page break after %s in %s. Nothing was written.", after, full)}, nil
	}
	backup, err := editZipEntryInPlace(full, docxDocumentEntry, newDoc)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_page_break: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Inserted a page break after %s in %s. Backup of the original saved as %s.", after, full, backup)}, nil
}

func formatDocxParagraphProperties(paragraph []byte, style, alignment string) []byte {
	pPr := extractBlock(paragraph, "w:pPr")
	updated := append([]byte(nil), pPr...)
	if style != "" {
		updated = upsertDocxProperty(updated, "w:pPr", "w:pStyle", []byte(`<w:pStyle w:val="`+xmlEscapeText(style)+`"/>`))
	}
	if alignment != "" {
		updated = upsertDocxProperty(updated, "w:pPr", "w:jc", []byte(`<w:jc w:val="`+xmlEscapeText(alignment)+`"/>`))
	}
	if len(pPr) > 0 {
		at := bytes.Index(paragraph, pPr)
		return spliceBytes(paragraph, at, at+len(pPr), updated)
	}
	openEnd := bytes.IndexByte(paragraph, '>')
	if openEnd < 0 {
		return paragraph
	}
	return spliceBytes(paragraph, openEnd+1, openEnd+1, updated)
}

func formatDocxParagraphRuns(paragraph []byte, bold, italic, underline *bool, fontSize float64, color string) []byte {
	type span struct{ start, end int }
	var runs []span
	for pos := 0; pos < len(paragraph); {
		start := findExactTag(paragraph, pos, "w:r")
		if start < 0 {
			break
		}
		gt := bytes.IndexByte(paragraph[start:], '>')
		if gt < 0 {
			break
		}
		openEnd := start + gt + 1
		closeAt := bytes.Index(paragraph[openEnd:], []byte("</w:r>"))
		if closeAt < 0 {
			break
		}
		end := openEnd + closeAt + len("</w:r>")
		frag := paragraph[start:end]
		if bytes.Contains(frag, []byte("<w:t")) || bytes.Contains(frag, []byte("<w:delText")) {
			runs = append(runs, span{start: start, end: end})
		}
		pos = end
	}
	for i := len(runs) - 1; i >= 0; i-- {
		run := paragraph[runs[i].start:runs[i].end]
		rPr := extractBlock(run, "w:rPr")
		updated := append([]byte(nil), rPr...)
		if bold != nil {
			updated = upsertDocxProperty(updated, "w:rPr", "w:b", docxToggleXML("w:b", *bold))
		}
		if italic != nil {
			updated = upsertDocxProperty(updated, "w:rPr", "w:i", docxToggleXML("w:i", *italic))
		}
		if underline != nil {
			value := "none"
			if *underline {
				value = "single"
			}
			updated = upsertDocxProperty(updated, "w:rPr", "w:u", []byte(`<w:u w:val="`+value+`"/>`))
		}
		if fontSize != 0 {
			halfPoints := int(math.Round(fontSize * 2))
			updated = upsertDocxProperty(updated, "w:rPr", "w:sz", []byte(fmt.Sprintf(`<w:sz w:val="%d"/>`, halfPoints)))
			updated = upsertDocxProperty(updated, "w:rPr", "w:szCs", []byte(fmt.Sprintf(`<w:szCs w:val="%d"/>`, halfPoints)))
		}
		if color != "" {
			updated = upsertDocxProperty(updated, "w:rPr", "w:color", []byte(`<w:color w:val="`+color+`"/>`))
		}
		if len(rPr) > 0 {
			at := bytes.Index(run, rPr)
			run = spliceBytes(run, at, at+len(rPr), updated)
		} else {
			openEnd := bytes.IndexByte(run, '>')
			if openEnd >= 0 {
				run = spliceBytes(run, openEnd+1, openEnd+1, updated)
			}
		}
		paragraph = spliceBytes(paragraph, runs[i].start, runs[i].end, run)
	}
	return paragraph
}

func docxToggleXML(tag string, enabled bool) []byte {
	if enabled {
		return []byte("<" + tag + "/>")
	}
	return []byte("<" + tag + ` w:val="0"/>`)
}

func upsertDocxProperty(container []byte, containerTag, propertyTag string, property []byte) []byte {
	if len(container) == 0 {
		return append(append([]byte("<"+containerTag+">"), property...), []byte("</"+containerTag+">")...)
	}
	if start, end := docxElementSpan(container, propertyTag); start >= 0 {
		return spliceBytes(container, start, end, property)
	}
	gt := bytes.IndexByte(container, '>')
	if gt < 0 {
		return container
	}
	if gt > 0 && container[gt-1] == '/' {
		var out bytes.Buffer
		out.Write(container[:gt-1])
		out.WriteByte('>')
		out.Write(property)
		out.WriteString("</" + containerTag + ">")
		return out.Bytes()
	}
	return spliceBytes(container, gt+1, gt+1, property)
}

func docxElementSpan(data []byte, tag string) (int, int) {
	start := findExactTag(data, 0, tag)
	if start < 0 {
		return -1, -1
	}
	gt := bytes.IndexByte(data[start:], '>')
	if gt < 0 {
		return -1, -1
	}
	openEnd := start + gt + 1
	if data[openEnd-2] == '/' {
		return start, openEnd
	}
	closeTag := []byte("</" + tag + ">")
	closeAt := bytes.Index(data[openEnd:], closeTag)
	if closeAt < 0 {
		return -1, -1
	}
	return start, openEnd + closeAt + len(closeTag)
}
