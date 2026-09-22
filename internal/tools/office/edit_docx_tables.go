package office

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	maxDocxTableRows = 500
	maxDocxTableCols = 40
)

type docxContentStats struct {
	Paragraphs int
	Tables     int
	Rows       int
}

type docxElementLocation struct {
	selector   string
	start, end int
}

type docxTableLocation struct {
	docxElementLocation
	rows []docxElementLocation
}

var (
	docxTableSelectorRe = regexp.MustCompile(`(?i)^/body/tbl\[([1-9][0-9]*)\]$`)
	docxRowSelectorRe   = regexp.MustCompile(`(?i)^/body/tbl\[([1-9][0-9]*)\]/tr\[([1-9][0-9]*)\]$`)
)

// buildDocxBlocksXML accepts ordinary paragraph-per-line input plus Markdown
// pipe tables. A separator row (|---|---|) distinguishes a real table from a
// paragraph which merely contains a pipe character.
func buildDocxBlocksXML(text, style string) ([]byte, docxContentStats) {
	return buildDocxBlocks(text, func(line string) []byte {
		xml, _ := buildParagraphsXML(line, style)
		return xml
	}, func(rows [][]string) []byte { return buildDocxTableXML(rows, true) })
}

func buildDocxBlocksMatchingDocument(doc []byte, text, style string) ([]byte, docxContentStats) {
	return buildDocxBlocks(text, func(line string) []byte {
		xml, _ := buildParagraphsMatchingDocument(doc, line, style)
		return xml
	}, func(rows [][]string) []byte { return buildDocxTableXML(rows, true) })
}

func buildDocxBlocksDesigned(text, style string, design docxDesign) ([]byte, docxContentStats) {
	if design.Name == "plain" {
		return buildDocxBlocksXML(text, style)
	}
	return buildDocxBlocks(text, func(line string) []byte {
		xml, _ := buildParagraphsXML(line, style)
		return xml
	}, func(rows [][]string) []byte { return buildDocxDesignedTableXML(rows, true, design) })
}

func buildDocxBlocks(text string, paragraph func(string) []byte, table func([][]string) []byte) ([]byte, docxContentStats) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out bytes.Buffer
	var stats docxContentStats
	for i := 0; i < len(lines); {
		if rows, consumed, ok := markdownTableAt(lines, i); ok {
			out.Write(table(rows))
			stats.Tables++
			stats.Rows += len(rows)
			i += consumed
			continue
		}
		out.Write(paragraph(lines[i]))
		stats.Paragraphs++
		i++
	}
	return out.Bytes(), stats
}

func markdownTableAt(lines []string, at int) ([][]string, int, bool) {
	if at+1 >= len(lines) {
		return nil, 0, false
	}
	header, ok := parseMarkdownPipeRow(lines[at])
	if !ok {
		return nil, 0, false
	}
	separator, ok := parseMarkdownPipeRow(lines[at+1])
	if !ok || len(separator) != len(header) || !isMarkdownTableSeparator(separator) {
		return nil, 0, false
	}
	rows := [][]string{header}
	i := at + 2
	for ; i < len(lines); i++ {
		row, rowOK := parseMarkdownPipeRow(lines[i])
		if !rowOK || len(row) != len(header) {
			break
		}
		rows = append(rows, row)
		if len(rows) >= maxDocxTableRows {
			break
		}
	}
	return rows, i - at, true
}

func parseDocxTableText(text string) ([][]string, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	rows := make([][]string, 0, len(lines))
	columns := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row []string
		if strings.Contains(line, "\t") {
			for _, cell := range strings.Split(line, "\t") {
				row = append(row, strings.TrimSpace(cell))
			}
		} else {
			var ok bool
			row, ok = parseMarkdownPipeRow(line)
			if !ok {
				return nil, fmt.Errorf("table row %q is neither pipe-separated nor tab-separated", line)
			}
		}
		if isMarkdownTableSeparator(row) {
			continue
		}
		if columns == 0 {
			columns = len(row)
			if columns == 0 || columns > maxDocxTableCols {
				return nil, fmt.Errorf("table has %d columns; allowed range is 1..%d", columns, maxDocxTableCols)
			}
		}
		if len(row) != columns {
			return nil, fmt.Errorf("table row has %d cells; expected %d", len(row), columns)
		}
		rows = append(rows, row)
		if len(rows) > maxDocxTableRows {
			return nil, fmt.Errorf("table exceeds %d rows", maxDocxTableRows)
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("table text contains no rows")
	}
	return rows, nil
}

func parseMarkdownPipeRow(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") {
		return nil, false
	}
	if strings.HasPrefix(trimmed, "|") {
		trimmed = strings.TrimPrefix(trimmed, "|")
	}
	if strings.HasSuffix(trimmed, "|") && !strings.HasSuffix(trimmed, `\|`) {
		trimmed = strings.TrimSuffix(trimmed, "|")
	}
	var cells []string
	var cell strings.Builder
	escaped := false
	for _, r := range trimmed {
		switch {
		case escaped:
			if r != '|' && r != '\\' {
				cell.WriteRune('\\')
			}
			cell.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteRune(r)
		}
	}
	if escaped {
		cell.WriteRune('\\')
	}
	cells = append(cells, strings.TrimSpace(cell.String()))
	return cells, len(cells) > 1
}

func isMarkdownTableSeparator(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		cell = strings.TrimPrefix(cell, ":")
		cell = strings.TrimSuffix(cell, ":")
		if len(cell) < 3 || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return true
}

func buildDocxTableXML(rows [][]string, header bool) []byte {
	cols := len(rows[0])
	gridWidth := 9000 / cols
	cellPct := 5000 / cols
	var out bytes.Buffer
	out.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/><w:tblW w:w="5000" w:type="pct"/><w:tblLayout w:type="autofit"/>`)
	out.WriteString(`<w:tblBorders><w:top w:val="single" w:sz="4" w:color="D9D9D9"/><w:left w:val="single" w:sz="4" w:color="D9D9D9"/><w:bottom w:val="single" w:sz="4" w:color="D9D9D9"/><w:right w:val="single" w:sz="4" w:color="D9D9D9"/><w:insideH w:val="single" w:sz="4" w:color="D9D9D9"/><w:insideV w:val="single" w:sz="4" w:color="D9D9D9"/></w:tblBorders>`)
	out.WriteString(`<w:tblCellMar><w:top w:w="100" w:type="dxa"/><w:left w:w="120" w:type="dxa"/><w:bottom w:w="100" w:type="dxa"/><w:right w:w="120" w:type="dxa"/></w:tblCellMar></w:tblPr><w:tblGrid>`)
	for range cols {
		fmt.Fprintf(&out, `<w:gridCol w:w="%d"/>`, gridWidth)
	}
	out.WriteString(`</w:tblGrid>`)
	for i, row := range rows {
		out.Write(buildDocxTableRowXML(row, header && i == 0, i, cellPct))
	}
	out.WriteString(`</w:tbl>`)
	return out.Bytes()
}

func buildDocxTableRowXML(row []string, header bool, rowIndex, cellPct int) []byte {
	var out bytes.Buffer
	out.WriteString(`<w:tr>`)
	if header {
		out.WriteString(`<w:trPr><w:tblHeader/></w:trPr>`)
	}
	for _, value := range row {
		fmt.Fprintf(&out, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="pct"/><w:vAlign w:val="center"/>`, cellPct)
		if header {
			out.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="1F4E78"/>`)
		} else if rowIndex%2 == 0 {
			out.WriteString(`<w:shd w:val="clear" w:color="auto" w:fill="F3F6FA"/>`)
		}
		out.WriteString(`</w:tcPr><w:p><w:pPr><w:spacing w:after="0"/>`)
		if header {
			out.WriteString(`<w:jc w:val="center"/>`)
		}
		out.WriteString(`</w:pPr><w:r>`)
		if header {
			out.WriteString(`<w:rPr><w:b/><w:color w:val="FFFFFF"/></w:rPr>`)
		}
		writeDocxInlineText(&out, strings.ReplaceAll(value, "<br>", "\n"))
		out.WriteString(`</w:r></w:p></w:tc>`)
	}
	out.WriteString(`</w:tr>`)
	return out.Bytes()
}

func collectDocxTableLocations(doc []byte) ([]docxTableLocation, error) {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	var locations []docxTableLocation
	inBody, tableDepth := false, 0
	tableIndex, rowIndex := 0, 0
	var current *docxTableLocation
	var rowStart int
	for {
		before := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse document tables: %w", err)
		}
		switch element := tok.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "body":
				inBody = true
			case "tbl":
				if inBody && tableDepth == 0 {
					tableIndex++
					rowIndex = 0
					current = &docxTableLocation{docxElementLocation: docxElementLocation{selector: fmt.Sprintf("/body/tbl[%d]", tableIndex), start: before}}
				}
				if inBody {
					tableDepth++
				}
			case "tr":
				if current != nil && tableDepth == 1 {
					rowIndex++
					rowStart = before
				}
			}
		case xml.EndElement:
			switch element.Name.Local {
			case "tr":
				if current != nil && tableDepth == 1 && rowStart >= 0 {
					current.rows = append(current.rows, docxElementLocation{selector: fmt.Sprintf("%s/tr[%d]", current.selector, rowIndex), start: rowStart, end: int(dec.InputOffset())})
					rowStart = -1
				}
			case "tbl":
				if inBody && tableDepth == 1 && current != nil {
					current.end = int(dec.InputOffset())
					locations = append(locations, *current)
					current = nil
				}
				if inBody && tableDepth > 0 {
					tableDepth--
				}
			case "body":
				inBody = false
			}
		}
	}
	return locations, nil
}

func findDocxTableLocation(doc []byte, selector string) (docxTableLocation, error) {
	selector = strings.TrimSpace(selector)
	if !docxTableSelectorRe.MatchString(selector) {
		return docxTableLocation{}, fmt.Errorf("invalid table selector %q (want /body/tbl[N])", selector)
	}
	locations, err := collectDocxTableLocations(doc)
	if err != nil {
		return docxTableLocation{}, err
	}
	for _, location := range locations {
		if strings.EqualFold(location.selector, selector) {
			return location, nil
		}
	}
	return docxTableLocation{}, fmt.Errorf("table selector %q was not found; use read_docx with selectors=true", selector)
}

func findDocxInsertionPoint(doc []byte, selector string) (int, error) {
	selector = strings.TrimSpace(selector)
	if docxTableSelectorRe.MatchString(selector) {
		table, err := findDocxTableLocation(doc, selector)
		return table.end, err
	}
	paragraph, err := findDocxParagraphLocation(doc, selector)
	if err != nil {
		return 0, err
	}
	if docxOffsetInsideTable(doc, paragraph.start) {
		return 0, fmt.Errorf("cannot insert document blocks after table-cell paragraph %q; use replace_at for the cell or insert after /body/tbl[N]", selector)
	}
	return paragraph.end, nil
}

func countTopLevelCells(row []byte) (int, error) {
	dec := xml.NewDecoder(bytes.NewReader(row))
	depth, count := 0, 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return count, nil
		}
		if err != nil {
			return 0, err
		}
		switch element := tok.(type) {
		case xml.StartElement:
			if element.Name.Local == "tbl" {
				depth++
			} else if element.Name.Local == "tc" && depth == 0 {
				count++
			}
		case xml.EndElement:
			if element.Name.Local == "tbl" && depth > 0 {
				depth--
			}
		}
	}
}
