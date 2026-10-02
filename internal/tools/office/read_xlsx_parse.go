package office

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
)

func (t *ReadXlsxTool) renderSheet(data []byte, shared []string, maxCells int) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out strings.Builder
	rowIndex := 0
	cellsSeen := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse xml: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "row" {
			continue
		}
		rowCells, err := parseRow(dec, shared, &cellsSeen, maxCells)
		if err != nil {
			return "", err
		}
		if len(rowCells) == 0 {
			continue
		}
		if rowIndex > 0 {
			out.WriteString("\n")
		}
		out.WriteString(strings.Join(rowCells, " | "))
		rowIndex++
		if int64(out.Len()) > t.MaxOutputBytes {
			return "", fmt.Errorf("rendered text exceeds %d bytes", t.MaxOutputBytes)
		}
	}
	return out.String(), nil
}

// parseRow hand-walks the children of <row>
// and returns each cell's resolved text value.
// Cells are emitted in source order. The cell
// count is bumped per cell; if it exceeds
// maxCells, we stop and signal truncation via
// a trailing "..." marker in the final row.
func parseRow(dec *xml.Decoder, shared []string, cellsSeen *int, maxCells int) ([]string, error) {
	var row []string
	for {
		tok, err := dec.Token()
		if err != nil {
			return row, fmt.Errorf("parse row: %w", err)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			if se.Name.Local != "c" {
				// Unknown child of <row>:
				// drain it.
				if err := dec.Skip(); err != nil {
					return row, fmt.Errorf("skip %s: %w", se.Name.Local, err)
				}
				continue
			}
			if *cellsSeen >= maxCells {
				// Cap reached. We still
				// need to drain the rest
				// of the row so the
				// decoder stays in sync.
				if err := dec.Skip(); err != nil {
					return row, fmt.Errorf("skip c: %w", err)
				}
				continue
			}
			val, err := parseCell(dec, se, shared)
			if err != nil {
				return row, err
			}
			row = append(row, val)
			*cellsSeen++
		case xml.EndElement:
			if se.Name.Local == "row" {
				return row, nil
			}
		}
	}
}

// parseCell reads a <c> element and returns
// the resolved text. Supported types:
//   - t="s"       → shared string by index
//   - t="inlineStr" → inline <is><t>...</t></is>
//   - t="b"       → boolean (0/1)
//   - (no type)   → number
//   - t="str"     → formula string result
//   - t="e"       → error code (passed through)
func parseCell(dec *xml.Decoder, se xml.StartElement, shared []string) (string, error) {
	var t string
	for _, a := range se.Attr {
		if a.Name.Local == "t" {
			t = a.Value
			break
		}
	}
	switch t {
	case "s":
		idx, err := readCellIntValue(dec, se)
		if err != nil {
			return "", err
		}
		if idx < 0 || idx >= len(shared) {
			return "", nil
		}
		return shared[idx], nil
	case "inlineStr":
		return readInlineString(dec, se)
	case "b":
		n, err := readCellIntValue(dec, se)
		if err != nil {
			return "", err
		}
		if n == 1 {
			return "TRUE", nil
		}
		return "FALSE", nil
	case "str", "e":
		// Formula string result / error code:
		// the value is plain text.
		return readCellTextValue(dec, se)
	default:
		// Number, or no type.
		return readCellTextValue(dec, se)
	}
}

// readCellIntValue reads the <v>...</v>
// content of a cell and returns it as an int.
func readCellIntValue(dec *xml.Decoder, se xml.StartElement) (int, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0, fmt.Errorf("parse c: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "v" {
				var v struct {
					Text string `xml:",chardata"`
				}
				if err := dec.DecodeElement(&v, &t); err != nil {
					return 0, fmt.Errorf("parse v: %w", err)
				}
				n, err := strconv.Atoi(strings.TrimSpace(v.Text))
				if err != nil {
					return 0, fmt.Errorf("parse v %q: %w", v.Text, err)
				}
				return n, nil
			}
			// Skip other children (e.g. <is>,
			// <f> formula).
			if err := dec.Skip(); err != nil {
				return 0, fmt.Errorf("skip %s: %w", t.Name.Local, err)
			}
		case xml.EndElement:
			if t.Name.Local == "c" {
				return 0, nil
			}
		}
	}
}

// readCellTextValue reads the <v>...</v>
// content of a cell and returns it verbatim.
func readCellTextValue(dec *xml.Decoder, se xml.StartElement) (string, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("parse c: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "v" {
				var v struct {
					Text string `xml:",chardata"`
				}
				if err := dec.DecodeElement(&v, &t); err != nil {
					return "", fmt.Errorf("parse v: %w", err)
				}
				return strings.TrimSpace(v.Text), nil
			}
			if err := dec.Skip(); err != nil {
				return "", fmt.Errorf("skip %s: %w", t.Name.Local, err)
			}
		case xml.EndElement:
			if t.Name.Local == "c" {
				return "", nil
			}
		}
	}
}

// readInlineString reads an <is><t>...</t></is>
// inline string and returns the text. The
// structure inside <c t="inlineStr"> is
//
//	<is><t>text</t></is>
//
// — we step past the <is> wrapper and pull
// the <t> out by decoding the <is> element.
func readInlineString(dec *xml.Decoder, se xml.StartElement) (string, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("parse c: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "is" {
				var is struct {
					Text string `xml:"t"`
				}
				if err := dec.DecodeElement(&is, &t); err != nil {
					return "", fmt.Errorf("parse is: %w", err)
				}
				return is.Text, nil
			}
			if err := dec.Skip(); err != nil {
				return "", fmt.Errorf("skip %s: %w", t.Name.Local, err)
			}
		case xml.EndElement:
			if t.Name.Local == "c" {
				return "", nil
			}
		}
	}
}

// IsXlsx reports whether the file at path
// looks like an .xlsx (a zip with the
// xl/workbook.xml entry). Used by callers
// that want to disambiguate from other zip
// formats like .docx and .pptx.
func IsXlsx(path string) bool {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return false
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == "xl/workbook.xml" {
			return true
		}
	}
	return false
}

var errXlsxEntryNotFound = errors.New("xlsx zip entry not found")

// readXlsxZipEntry shares one archive reader for workbook metadata and data,
// retaining both declared-size and runtime bounds for every entry.
func readXlsxZipEntry(zr *zip.Reader, name string, maxBytes int64) ([]byte, error) {
	var target *zip.File
	for _, f := range zr.File {
		if f.Name == name {
			target = f
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("entry %q not found: %w", name, errXlsxEntryNotFound)
	}
	if target.UncompressedSize64 > uint64(maxBytes) {
		return nil, fmt.Errorf("entry %q is too large: %d bytes (declared) > %d cap", name, target.UncompressedSize64, maxBytes)
	}
	rc, err := target.Open()
	if err != nil {
		return nil, fmt.Errorf("open entry: %w", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read entry: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("entry %q exceeded %d bytes during read", name, maxBytes)
	}
	return data, nil
}

type xlsxWorkbookSheet struct {
	Name           string `xml:"name,attr"`
	RelationshipID string `xml:"id,attr"`
}

// resolveSheetEntry keeps the existing default and physical-number selection.
// Logical names use workbook relationships; legacy direct entry names still
// work when no logical name matches, without masking malformed metadata.
func (t *ReadXlsxTool) resolveSheetEntry(zr *zip.Reader, sheet string) (string, error) {
	if sheet == "" {
		return "xl/worksheets/sheet1.xml", nil
	}
	if n, err := strconv.Atoi(sheet); err == nil {
		if n < 1 {
			return "", fmt.Errorf("sheet index must be >= 1, got %d", n)
		}
		return fmt.Sprintf("xl/worksheets/sheet%d.xml", n), nil
	}
	if strings.ContainsAny(sheet, `/\`) {
		return "", fmt.Errorf("invalid sheet name %q", sheet)
	}
	var workbook struct {
		XMLName xml.Name            `xml:"workbook"`
		Sheets  []xlsxWorkbookSheet `xml:"sheets>sheet"`
	}
	data, err := readXlsxZipEntry(zr, "xl/workbook.xml", t.MaxXlsxBytes)
	if err != nil && !errors.Is(err, errXlsxEntryNotFound) {
		return "", err
	}
	if err == nil && len(bytes.TrimSpace(data)) != 0 {
		if err := xml.Unmarshal(data, &workbook); err != nil {
			return "", fmt.Errorf("parse workbook.xml: %w", err)
		}
	}
	for _, s := range workbook.Sheets {
		if s.Name != sheet {
			continue
		}
		if s.RelationshipID == "" {
			return "", fmt.Errorf("sheet %q has no worksheet relationship", sheet)
		}
		data, err := readXlsxZipEntry(zr, "xl/_rels/workbook.xml.rels", t.MaxXlsxBytes)
		if err != nil {
			return "", fmt.Errorf("sheet %q relationship %q: %w", sheet, s.RelationshipID, err)
		}
		var rels struct {
			XMLName xml.Name `xml:"Relationships"`
			Items   []struct {
				ID         string `xml:"Id,attr"`
				Type       string `xml:"Type,attr"`
				Target     string `xml:"Target,attr"`
				TargetMode string `xml:"TargetMode,attr"`
			} `xml:"Relationship"`
		}
		if err := xml.Unmarshal(data, &rels); err != nil {
			return "", fmt.Errorf("parse workbook.xml.rels: %w", err)
		}
		for _, rel := range rels.Items {
			if rel.ID != s.RelationshipID {
				continue
			}
			if strings.EqualFold(rel.TargetMode, "External") {
				return "", fmt.Errorf("sheet %q uses an external worksheet relationship", sheet)
			}
			if rel.TargetMode != "" && !strings.EqualFold(rel.TargetMode, "Internal") {
				return "", fmt.Errorf("sheet %q has invalid worksheet relationship mode %q", sheet, rel.TargetMode)
			}
			if !strings.HasSuffix(rel.Type, "/worksheet") {
				return "", fmt.Errorf("sheet %q relationship is not a worksheet", sheet)
			}
			entry, err := xlsxWorksheetTarget(rel.Target)
			if err != nil {
				return "", fmt.Errorf("sheet %q: %w", sheet, err)
			}
			return entry, nil
		}
		return "", fmt.Errorf("sheet %q worksheet relationship %q not found", sheet, s.RelationshipID)
	}
	// Older tools accepted physical entry names, including Sheet1.xml. Preserve
	// existing archives of this form while prioritizing genuine workbook names.
	legacy := "xl/worksheets/" + strings.TrimSuffix(sheet, ".xml") + ".xml"
	for _, f := range zr.File {
		if f.Name == legacy {
			return legacy, nil
		}
	}
	return "", fmt.Errorf("sheet %q not found; available sheets: %s", sheet, xlsxAvailableSheetNames(workbook.Sheets))
}

func xlsxWorksheetTarget(target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", fmt.Errorf("invalid worksheet target %q", target)
	}
	if u.Path == "" || strings.ContainsAny(u.Path, `\`) || strings.HasSuffix(u.Path, "/") {
		return "", fmt.Errorf("invalid worksheet target %q", target)
	}
	var entry string
	if strings.HasPrefix(u.Path, "/") {
		entry = path.Clean(strings.TrimPrefix(u.Path, "/"))
	} else {
		entry = path.Clean(path.Join("xl", u.Path))
	}
	if entry == "." || entry == ".." || strings.HasPrefix(entry, "../") || path.IsAbs(entry) {
		return "", fmt.Errorf("invalid worksheet target %q", target)
	}
	return entry, nil
}

func xlsxAvailableSheetNames(sheets []xlsxWorkbookSheet) string {
	if len(sheets) == 0 {
		return "(none listed in workbook metadata)"
	}
	const maxNames = 8
	names := make([]string, 0, maxNames)
	for i, s := range sheets {
		if i == maxNames {
			break
		}
		name, count, end := s.Name, 0, len(s.Name)
		for offset := range name {
			if count == 64 {
				end = offset
				break
			}
			count++
		}
		if end < len(name) {
			name = name[:end] + "…"
		}
		names = append(names, strconv.Quote(name))
	}
	result := strings.Join(names, ", ")
	if len(sheets) > len(names) {
		result += fmt.Sprintf(" (and %d more)", len(sheets)-len(names))
	}
	return result
}
