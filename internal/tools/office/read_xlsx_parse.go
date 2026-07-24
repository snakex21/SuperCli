package office

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
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
