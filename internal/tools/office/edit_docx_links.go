package office

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"
)

const docxHyperlinkRelationship = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"

func (t *EditDocxTool) doInsertLink(full string, p editDocxArgs) (Result, error) {
	label := strings.TrimSpace(p.Text)
	if label == "" {
		err := fmt.Errorf("edit_docx insert_link: 'text' is required as the visible link label")
		return Result{Err: err}, err
	}
	target := strings.TrimSpace(p.URL)
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "mailto") {
		err = fmt.Errorf("edit_docx insert_link: url must be an absolute http, https or mailto URL")
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	rels, _, err := readOptionalZipEntry(full, docxRelsEntry, t.MaxDocxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_link: relationships: %w", err)}, err
	}
	relID := nextDocxRelationshipID(rels)
	rels = appendDocxExternalRelationship(rels, relID, docxHyperlinkRelationship, target)
	doc = ensureDocxDrawingNamespaces(doc)
	linkXML := buildDocxHyperlinkXML(relID, label)
	after := strings.TrimSpace(p.After)
	var newDoc []byte
	if after == "" || strings.EqualFold(after, "end") {
		newDoc, err = docxInsertBeforeBodyEnd(doc, linkXML)
		after = "end"
	} else {
		var insertAt int
		insertAt, err = findDocxInsertionPoint(doc, after)
		if err == nil {
			newDoc = spliceBytes(doc, insertAt, insertAt, linkXML)
		}
	}
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_link: %w", err)}, err
	}
	if err := validateXMLUpdates(map[string][]byte{docxDocumentEntry: newDoc, docxRelsEntry: rels}); err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_link: %w", err)}, err
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would insert link %q after %s in %s. Nothing was written.", label, after, full)}, nil
	}
	backup, err := editZipEntriesInPlace(full, map[string][]byte{docxDocumentEntry: newDoc, docxRelsEntry: rels})
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_link: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Inserted link %q after %s in %s. Backup of the original saved as %s.", label, after, full, backup)}, nil
}

func appendDocxExternalRelationship(rels []byte, id, relType, target string) []byte {
	if len(rels) == 0 {
		rels = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="` + relNS + `"></Relationships>`)
	}
	closeAt := bytes.LastIndex(rels, []byte("</Relationships>"))
	if closeAt < 0 {
		return rels
	}
	rel := fmt.Sprintf(`<Relationship Id="%s" Type="%s" Target="%s" TargetMode="External"/>`, xmlEscapeText(id), xmlEscapeText(relType), xmlEscapeText(target))
	return spliceBytes(rels, closeAt, closeAt, []byte(rel))
}

func buildDocxHyperlinkXML(relID, label string) []byte {
	var out bytes.Buffer
	out.WriteString(`<w:p><w:hyperlink r:id="` + xmlEscapeText(relID) + `" w:history="1"><w:r><w:rPr><w:color w:val="0563C1"/><w:u w:val="single"/></w:rPr>`)
	writeRunText(&out, label)
	out.WriteString(`</w:r></w:hyperlink></w:p>`)
	return out.Bytes()
}
