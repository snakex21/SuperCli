package office

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	docxImageRelationship = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
	maxDocxImageBytes     = 50 * 1024 * 1024
)

var docxDrawingIDRe = regexp.MustCompile(`<wp:docPr\b[^>]*\bid=["']([0-9]+)["']`)

func (t *EditDocxTool) doInsertImage(full string, p editDocxArgs) (Result, error) {
	if strings.TrimSpace(p.ImagePath) == "" {
		err := fmt.Errorf("edit_docx insert_image: 'image_path' is required")
		return Result{Err: err}, err
	}
	imagePath, err := resolveSandboxed(t.BaseDir, p.ImagePath)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_image: %w", err)}, err
	}
	info, err := os.Stat(imagePath)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_image: %w", err)}, err
	}
	if info.IsDir() || info.Size() <= 0 || info.Size() > maxDocxImageBytes {
		err := fmt.Errorf("edit_docx insert_image: image size %d is outside 1..%d bytes", info.Size(), maxDocxImageBytes)
		return Result{Err: err}, err
	}
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_image: read image: %w", err)}, err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(imageData))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		err = fmt.Errorf("edit_docx insert_image: image must be a valid PNG or JPEG")
		return Result{Err: err}, err
	}
	var extension, contentType string
	switch format {
	case "png":
		extension, contentType = "png", "image/png"
	case "jpeg":
		extension, contentType = "jpg", "image/jpeg"
	default:
		err = fmt.Errorf("edit_docx insert_image: unsupported image format %q (use PNG or JPEG)", format)
		return Result{Err: err}, err
	}
	widthCM := p.WidthCM
	if widthCM == 0 {
		widthCM = 14.5
	}
	if widthCM < 0.5 || widthCM > 30 {
		err := fmt.Errorf("edit_docx insert_image: width_cm %.2f is outside 0.5..30", widthCM)
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	rels, _, err := readOptionalZipEntry(full, docxRelsEntry, t.MaxDocxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_image: relationships: %w", err)}, err
	}
	types, _, err := readOptionalZipEntry(full, docxTypesEntry, t.MaxDocxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_image: content types: %w", err)}, err
	}
	mediaName, err := nextDocxMediaName(full, extension)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_image: %w", err)}, err
	}
	relID := nextDocxRelationshipID(rels)
	rels = appendDocxRelationship(rels, relID, docxImageRelationship, "media/"+filepath.Base(mediaName))
	types = ensureDocxDefaultContentType(types, extension, contentType)
	doc = ensureDocxDrawingNamespaces(doc)
	drawingID := nextDocxDrawingID(doc)
	alt := strings.TrimSpace(p.AltText)
	if alt == "" {
		alt = filepath.Base(imagePath)
	}
	widthEMU := int64(widthCM * 360000)
	heightEMU := widthEMU * int64(config.Height) / int64(config.Width)
	content := buildDocxInlineImageXML(relID, drawingID, widthEMU, heightEMU, filepath.Base(imagePath), alt)
	if strings.TrimSpace(p.Text) != "" {
		caption, _ := buildParagraphsMatchingDocument(doc, p.Text, "")
		content = append(content, caption...)
	}
	after := strings.TrimSpace(p.After)
	var newDoc []byte
	if after == "" || strings.EqualFold(after, "end") {
		newDoc, err = docxInsertBeforeBodyEnd(doc, content)
		after = "end"
	} else {
		var insertAt int
		insertAt, err = findDocxInsertionPoint(doc, after)
		if err == nil {
			newDoc = spliceBytes(doc, insertAt, insertAt, content)
		}
	}
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_image: %w", err)}, err
	}
	updates := map[string][]byte{
		docxDocumentEntry: newDoc,
		docxRelsEntry:     rels,
		docxTypesEntry:    types,
		mediaName:         imageData,
	}
	if err := validateXMLUpdates(map[string][]byte{
		docxDocumentEntry: newDoc,
		docxRelsEntry:     rels,
		docxTypesEntry:    types,
	}); err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_image: %w", err)}, err
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would embed %s at %.2f cm wide after %s in %s. Nothing was written.", p.ImagePath, widthCM, after, full)}, nil
	}
	backup, err := editZipEntriesInPlace(full, updates)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_image: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Embedded %s at %.2f cm wide after %s in %s with alt text %q. Backup of the original saved as %s.", p.ImagePath, widthCM, after, full, alt, backup)}, nil
}

func nextDocxMediaName(path, extension string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open document package: %w", err)
	}
	defer zr.Close()
	used := make(map[string]bool)
	for _, file := range zr.File {
		used[strings.ToLower(file.Name)] = true
	}
	for n := 1; n < 1000000; n++ {
		name := fmt.Sprintf("word/media/image%d.%s", n, extension)
		if !used[strings.ToLower(name)] {
			return name, nil
		}
	}
	return "", fmt.Errorf("cannot allocate a media filename")
}

func nextDocxRelationshipID(rels []byte) string {
	next := 1
	for _, match := range rIDPattern.FindAllSubmatch(rels, -1) {
		value, _ := strconv.Atoi(string(match[1]))
		if value >= next {
			next = value + 1
		}
	}
	return fmt.Sprintf("rId%d", next)
}

func appendDocxRelationship(rels []byte, id, relType, target string) []byte {
	if len(rels) == 0 {
		rels = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="` + relNS + `"></Relationships>`)
	}
	closeAt := bytes.LastIndex(rels, []byte("</Relationships>"))
	if closeAt < 0 {
		return rels
	}
	rel := fmt.Sprintf(`<Relationship Id="%s" Type="%s" Target="%s"/>`, xmlEscapeText(id), xmlEscapeText(relType), xmlEscapeText(target))
	return spliceBytes(rels, closeAt, closeAt, []byte(rel))
}

func ensureDocxDefaultContentType(types []byte, extension, contentType string) []byte {
	if len(types) == 0 {
		types = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`)
	}
	if bytes.Contains(types, []byte(`Extension="`+extension+`"`)) || bytes.Contains(types, []byte(`Extension='`+extension+`'`)) {
		return types
	}
	closeAt := bytes.LastIndex(types, []byte("</Types>"))
	if closeAt < 0 {
		return types
	}
	entry := fmt.Sprintf(`<Default Extension="%s" ContentType="%s"/>`, xmlEscapeText(extension), xmlEscapeText(contentType))
	return spliceBytes(types, closeAt, closeAt, []byte(entry))
}

func ensureDocxDrawingNamespaces(doc []byte) []byte {
	namespaces := [][2]string{
		{"r", "http://schemas.openxmlformats.org/officeDocument/2006/relationships"},
		{"wp", "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"},
		{"a", "http://schemas.openxmlformats.org/drawingml/2006/main"},
		{"pic", "http://schemas.openxmlformats.org/drawingml/2006/picture"},
	}
	for _, namespace := range namespaces {
		open := bytes.Index(doc, []byte("<w:document"))
		if open < 0 {
			return doc
		}
		gt := bytes.IndexByte(doc[open:], '>')
		if gt < 0 {
			return doc
		}
		gt += open
		if bytes.Contains(doc[open:gt], []byte("xmlns:"+namespace[0]+"=")) {
			continue
		}
		attr := fmt.Sprintf(` xmlns:%s="%s"`, namespace[0], namespace[1])
		doc = spliceBytes(doc, gt, gt, []byte(attr))
	}
	return doc
}

func nextDocxDrawingID(doc []byte) int {
	next := 1
	for _, match := range docxDrawingIDRe.FindAllSubmatch(doc, -1) {
		value, _ := strconv.Atoi(string(match[1]))
		if value >= next {
			next = value + 1
		}
	}
	return next
}

func buildDocxInlineImageXML(relID string, drawingID int, widthEMU, heightEMU int64, name, alt string) []byte {
	name = xmlEscapeText(name)
	alt = xmlEscapeText(alt)
	return []byte(fmt.Sprintf(`<w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%d" cy="%d"/><wp:effectExtent l="0" t="0" r="0" b="0"/><wp:docPr id="%d" name="%s" descr="%s"/><wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:nvPicPr><pic:cNvPr id="0" name="%s" descr="%s"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`, widthEMU, heightEMU, drawingID, name, alt, name, alt, xmlEscapeText(relID), widthEMU, heightEMU))
}
