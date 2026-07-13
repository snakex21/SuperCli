// edit_xlsx.go implements the edit_xlsx tool: pure-Go editing
// of Excel .xlsx files (set cell values, append rows). An
// .xlsx is a zip archive; cell data lives in
// xl/worksheets/sheetN.xml. The tool rewrites ONLY that one
// entry and copies every other zip entry byte-for-byte, so
// styles, charts, other sheets and metadata survive untouched.
//
// Editing strategy: byte splicing on the sheet XML, never a
// full re-encode (encoding/xml round-trips mangle the
// namespaces Excel expects). New/changed string cells are
// written as inline strings (t="inlineStr"), so the shared
// strings table never needs rewriting. Numbers are written as
// numeric cells.
//
// LIMITATIONS (kept deliberately small for v1, documented in
// the tool description): formulas cannot be authored. Setting
// a cell replaces its formula with a plain value, but retains
// its style index; new cells inherit the nearest column style.
// The stored <dimension> hint is not updated (Excel recalculates
// it on open); rows are addressed by their r="N" attribute,
// which every mainstream producer writes.
//
// Safety: same protocol as edit_docx — temp file + atomic
// swap, and an existing file is copied to "<name>.xlsx.bak"
// before being replaced.
package office

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"supercli/internal/tools/fileops"
)

// EditXlsxTool edits .xlsx files.
type EditXlsxTool struct {
	BaseDir      string
	MaxXlsxBytes int64
}

// NewEditXlsx returns an EditXlsxTool rooted at baseDir.
func NewEditXlsx(baseDir string) *EditXlsxTool {
	if baseDir == "" {
		baseDir = "."
	}
	return &EditXlsxTool{BaseDir: baseDir, MaxXlsxBytes: DefaultMaxXlsxBytes}
}

// Spec returns the Tool descriptor.
func (t *EditXlsxTool) Spec() Tool {
	return Tool{
		Name: "edit_xlsx",
		Description: "Edit an Excel .xlsx file. Pure Go, no Excel required. " +
			"Use this when the user wants to change a spreadsheet: fix a cell's value, fill in a few cells, " +
			"or add new data rows at the bottom. Actions: 'set_cell' (write one cell, e.g. sheet 1 cell B3; " +
			"pass numbers as JSON numbers so they stay numeric in Excel, strings stay text), " +
			"'append_row' (add one row of values after the last used row). " +
			"Safety: before the file is changed, a backup copy is saved next to it as '<name>.bak', and the " +
			"write is atomic — tell the user the backup exists if they want to undo. All other sheets, charts " +
			"and styles are preserved byte-for-byte. Existing cells retain their style; new cells and appended " +
			"rows inherit the nearest style in the same column so edits match the workbook. " +
			"LIMITATION: formulas cannot be authored; overwriting a formula replaces it with a plain value while " +
			"retaining the cell style. Do NOT use this for Word documents (use edit_docx), for " +
			"creating brand-new spreadsheets, or for bulk restructuring (read the data with read_xlsx first " +
			"and discuss with the user).",
		Schema: `{
  "type": "object",
  "properties": {
    "path":   {"type": "string", "description": "Path to the .xlsx file."},
    "action": {"type": "string", "enum": ["set_cell", "append_row"], "description": "What to do."},
    "sheet":  {"type": "string", "description": "Sheet number, 1-based (e.g. '1'). Defaults to 1."},
    "cell":   {"type": "string", "description": "set_cell: the cell reference, e.g. 'B3'."},
    "value":  {"description": "set_cell: the value. JSON number => numeric cell, JSON string => text cell, boolean => TRUE/FALSE."},
    "values": {"type": "array", "description": "append_row: the row's cell values, left to right (numbers/strings/booleans)."},
    "style_from": {"type": "string", "description": "set_cell: explicitly clone style from another cell on the same sheet, e.g. B2."},
    "style_from_row": {"type": "integer", "description": "append_row: explicitly clone per-column styles from this 1-based row."}
  },
  "required": ["path", "action"]
}`,
		Fn: t.Execute,
	}
}

type editXlsxArgs struct {
	Path         string            `json:"path"`
	Action       string            `json:"action"`
	Sheet        string            `json:"sheet"`
	Cell         string            `json:"cell"`
	Value        json.RawMessage   `json:"value"`
	Values       []json.RawMessage `json:"values"`
	StyleFrom    string            `json:"style_from"`
	StyleFromRow int               `json:"style_from_row"`
}

// Execute dispatches on action.
func (t *EditXlsxTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var p editXlsxArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return Result{Err: fmt.Errorf("edit_xlsx: bad args: %w", err)}, err
	}
	full, err := resolveSandboxed(t.BaseDir, p.Path)
	if err != nil {
		err = fmt.Errorf("edit_xlsx: %w", err)
		return Result{Err: err}, err
	}
	sheetN := 1
	if strings.TrimSpace(p.Sheet) != "" {
		n, err := strconv.Atoi(strings.TrimSpace(p.Sheet))
		if err != nil || n < 1 {
			err := fmt.Errorf("edit_xlsx: bad sheet %q (want a 1-based number)", p.Sheet)
			return Result{Err: err}, err
		}
		sheetN = n
	}
	entry := fmt.Sprintf("xl/worksheets/sheet%d.xml", sheetN)

	info, err := os.Stat(full)
	if err != nil {
		err = fmt.Errorf("edit_xlsx: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}
	if info.Size() > t.MaxXlsxBytes {
		err := fmt.Errorf("edit_xlsx: file too large: %d > %d", info.Size(), t.MaxXlsxBytes)
		return Result{Err: err}, err
	}
	sheetXML, err := readZipEntry(full, entry, t.MaxXlsxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_xlsx: read sheet %d: %w", sheetN, err)}, err
	}

	var newXML []byte
	var summary string
	switch p.Action {
	case "set_cell":
		if p.Cell == "" || len(p.Value) == 0 {
			err := fmt.Errorf("edit_xlsx set_cell: 'cell' and 'value' are required")
			return Result{Err: err}, err
		}
		newXML, err = xlsxSetCellWithStyle(sheetXML, p.Cell, p.Value, p.StyleFrom)
		if err != nil {
			return Result{Err: fmt.Errorf("edit_xlsx: %w", err)}, err
		}
		summary = fmt.Sprintf("Set cell %s on sheet %d of %s", strings.ToUpper(p.Cell), sheetN, full)
	case "append_row":
		if len(p.Values) == 0 {
			err := fmt.Errorf("edit_xlsx append_row: 'values' is required and must be non-empty")
			return Result{Err: err}, err
		}
		var rowNum int
		newXML, rowNum, err = xlsxAppendRowWithStyleRow(sheetXML, p.Values, p.StyleFromRow)
		if err != nil {
			return Result{Err: fmt.Errorf("edit_xlsx: %w", err)}, err
		}
		summary = fmt.Sprintf("Appended row %d (%d cell(s)) on sheet %d of %s", rowNum, len(p.Values), sheetN, full)
	default:
		err := fmt.Errorf("edit_xlsx: unknown action %q (want set_cell|append_row)", p.Action)
		return Result{Err: err}, err
	}

	backup, err := editZipEntryInPlace(full, entry, newXML)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_xlsx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("%s. Backup of the original saved as %s.", summary, backup)}, nil
}

// ---------------------------------------------------------------------------
// cell/row XML building
// ---------------------------------------------------------------------------

var cellRefRe = regexp.MustCompile(`^([A-Za-z]{1,3})([0-9]+)$`)

// splitCellRef splits "B3" into (column letters, row number).
func splitCellRef(ref string) (string, int, error) {
	m := cellRefRe.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return "", 0, fmt.Errorf("bad cell reference %q (want e.g. 'B3')", ref)
	}
	row, err := strconv.Atoi(m[2])
	if err != nil || row < 1 {
		return "", 0, fmt.Errorf("bad cell reference %q", ref)
	}
	return strings.ToUpper(m[1]), row, nil
}

// colToIndex converts "A" -> 1, "B" -> 2, "AA" -> 27.
func colToIndex(col string) int {
	n := 0
	for _, c := range col {
		n = n*26 + int(c-'A'+1)
	}
	return n
}

// indexToCol converts 1 -> "A", 27 -> "AA".
func indexToCol(n int) string {
	var sb []byte
	for n > 0 {
		n--
		sb = append([]byte{byte('A' + n%26)}, sb...)
		n /= 26
	}
	return string(sb)
}

// buildCellXML renders one <c> element for the given JSON
// value. Numbers become numeric cells, booleans t="b", and
// everything else an inline string (so sharedStrings.xml is
// never touched).
func buildCellXML(ref string, raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	// JSON number?
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return fmt.Sprintf(`<c r="%s"><v>%s</v></c>`, ref, s)
	}
	if s == "true" {
		return fmt.Sprintf(`<c r="%s" t="b"><v>1</v></c>`, ref)
	}
	if s == "false" {
		return fmt.Sprintf(`<c r="%s" t="b"><v>0</v></c>`, ref)
	}
	var str string
	if err := json.Unmarshal(raw, &str); err != nil {
		// Non-string JSON (object/array/null): store
		// its raw text so nothing is silently lost.
		str = s
	}
	return fmt.Sprintf(`<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, xmlEscapeText(str))
}

// xlsxRowSpan describes one <row> element's byte span and
// parsed row number.
type xlsxRowSpan struct {
	start, end  int // byte offsets in the sheet XML
	num         int // r="N" (or implied position)
	selfClosing bool
}

var rowAttrRe = regexp.MustCompile(`r="([0-9]+)"`)

// scanRows locates every <row ...> element inside sheetData.
func scanRows(doc []byte) ([]xlsxRowSpan, error) {
	var rows []xlsxRowSpan
	pos := 0
	implied := 0
	for {
		i := bytes.Index(doc[pos:], []byte("<row"))
		if i < 0 {
			return rows, nil
		}
		start := pos + i
		after := start + len("<row")
		if after < len(doc) && doc[after] != ' ' && doc[after] != '>' && doc[after] != '/' {
			pos = after
			continue
		}
		gt := bytes.IndexByte(doc[start:], '>')
		if gt < 0 {
			return nil, fmt.Errorf("malformed sheet xml: unclosed <row>")
		}
		openEnd := start + gt + 1
		span := xlsxRowSpan{start: start, selfClosing: doc[start+gt-1] == '/'}
		if span.selfClosing {
			span.end = openEnd
		} else {
			j := bytes.Index(doc[openEnd:], []byte("</row>"))
			if j < 0 {
				return nil, fmt.Errorf("malformed sheet xml: unclosed <row>")
			}
			span.end = openEnd + j + len("</row>")
		}
		implied++
		span.num = implied
		if m := rowAttrRe.FindSubmatch(doc[start:openEnd]); m != nil {
			if n, err := strconv.Atoi(string(m[1])); err == nil {
				span.num = n
				implied = n
			}
		}
		rows = append(rows, span)
		pos = span.end
	}
}

// sheetDataInsertPoint returns the byte offset just before
// </sheetData>, expanding a self-closing <sheetData/> first.
// Returns the (possibly modified) doc and the offset.
func sheetDataInsertPoint(doc []byte) ([]byte, int, error) {
	if i := bytes.Index(doc, []byte("<sheetData/>")); i >= 0 {
		expanded := append([]byte{}, doc[:i]...)
		expanded = append(expanded, []byte("<sheetData></sheetData>")...)
		expanded = append(expanded, doc[i+len("<sheetData/>"):]...)
		doc = expanded
	}
	i := bytes.Index(doc, []byte("</sheetData>"))
	if i < 0 {
		return nil, 0, fmt.Errorf("malformed sheet xml: no <sheetData>")
	}
	return doc, i, nil
}

// xlsxSetCell writes one cell value into the sheet XML.
func xlsxSetCell(doc []byte, ref string, value json.RawMessage) ([]byte, error) {
	return xlsxSetCellWithStyle(doc, ref, value, "")
}

func xlsxSetCellWithStyle(doc []byte, ref string, value json.RawMessage, styleFrom string) ([]byte, error) {
	col, rowNum, err := splitCellRef(ref)
	if err != nil {
		return nil, err
	}
	cellRef := col + strconv.Itoa(rowNum)

	doc, _, err = sheetDataInsertPoint(doc) // normalizes <sheetData/>
	if err != nil {
		return nil, err
	}
	rows, err := scanRows(doc)
	if err != nil {
		return nil, err
	}
	style := nearestColumnStyle(doc, rows, rowNum, col)
	if strings.TrimSpace(styleFrom) != "" {
		var found bool
		style, found, err = exactCellStyle(doc, rows, styleFrom)
		if err != nil {
			return nil, fmt.Errorf("style_from: %w", err)
		}
		if !found {
			return nil, fmt.Errorf("style_from cell %s does not exist", strings.ToUpper(strings.TrimSpace(styleFrom)))
		}
	}
	cellXML := applyCellStyle(buildCellXML(cellRef, value), style)
	for _, r := range rows {
		if r.num != rowNum {
			continue
		}
		return spliceCellIntoRow(doc, r, col, cellRef, cellXML)
	}
	// Row absent: insert a new row in numeric order.
	newRow := fmt.Sprintf(`<row r="%d">%s</row>`, rowNum, cellXML)
	insertAt := -1
	for _, r := range rows {
		if r.num > rowNum {
			insertAt = r.start
			break
		}
	}
	if insertAt < 0 {
		_, end, err := sheetDataInsertPoint(doc)
		if err != nil {
			return nil, err
		}
		insertAt = end
	}
	return spliceBytes(doc, insertAt, insertAt, []byte(newRow)), nil
}

// spliceCellIntoRow replaces or inserts the cell within an
// existing row span.
func spliceCellIntoRow(doc []byte, r xlsxRowSpan, col, cellRef, cellXML string) ([]byte, error) {
	if r.selfClosing {
		// <row r="3"/> -> <row r="3"><c .../></row>
		open := append([]byte{}, doc[r.start:r.end]...)
		open = bytes.TrimSuffix(open, []byte("/>"))
		newRow := string(open) + ">" + cellXML + "</row>"
		return spliceBytes(doc, r.start, r.end, []byte(newRow)), nil
	}
	rowBytes := doc[r.start:r.end]
	// Existing cell with the same ref?
	cells, err := scanCells(rowBytes)
	if err != nil {
		return nil, err
	}
	targetIdx := colToIndex(col)
	for _, c := range cells {
		if c.ref == cellRef {
			return spliceBytes(doc, r.start+c.start, r.start+c.end, []byte(cellXML)), nil
		}
	}
	// Insert before the first cell in a later column.
	for _, c := range cells {
		m := cellRefRe.FindStringSubmatch(c.ref)
		if m != nil && colToIndex(strings.ToUpper(m[1])) > targetIdx {
			at := r.start + c.start
			return spliceBytes(doc, at, at, []byte(cellXML)), nil
		}
	}
	// Append before </row>.
	at := r.end - len("</row>")
	return spliceBytes(doc, at, at, []byte(cellXML)), nil
}

// xlsxCellSpan is one <c> element's span within a row.
type xlsxCellSpan struct {
	start, end int
	ref        string
}

var cellStyleAttrRe = regexp.MustCompile(`(?:^|\s)s=("[^"]*"|'[^']*')`)

func cellStyleAttr(cell []byte) string {
	gt := bytes.IndexByte(cell, '>')
	if gt < 0 {
		return ""
	}
	m := cellStyleAttrRe.FindSubmatch(cell[:gt])
	if len(m) != 2 {
		return ""
	}
	return " s=" + string(m[1])
}

func applyCellStyle(cellXML, styleAttr string) string {
	if styleAttr == "" {
		return cellXML
	}
	gt := strings.IndexByte(cellXML, '>')
	if gt < 0 {
		return cellXML
	}
	return cellXML[:gt] + styleAttr + cellXML[gt:]
}

// nearestColumnStyle returns the style from the target cell itself or, for a
// new cell/row, from the closest populated cell in the same column. This is a
// deterministic workbook-side decision and costs the model no style tokens.
func nearestColumnStyle(doc []byte, rows []xlsxRowSpan, rowNum int, col string) string {
	return nearestColumnStyles(doc, rows, rowNum, []string{col})[col]
}

func nearestColumnStyles(doc []byte, rows []xlsxRowSpan, rowNum int, cols []string) map[string]string {
	type candidate struct {
		distance int
		style    string
	}
	wanted := make(map[string]bool, len(cols))
	best := make(map[string]candidate, len(cols))
	for _, col := range cols {
		wanted[col] = true
	}
	for _, row := range rows {
		rowBytes := doc[row.start:row.end]
		cells, err := scanCells(rowBytes)
		if err != nil {
			continue
		}
		for _, cell := range cells {
			m := cellRefRe.FindStringSubmatch(cell.ref)
			if m == nil {
				continue
			}
			col := strings.ToUpper(m[1])
			if !wanted[col] {
				continue
			}
			style := cellStyleAttr(rowBytes[cell.start:cell.end])
			if style == "" {
				continue
			}
			distance := row.num - rowNum
			if distance < 0 {
				distance = -distance
			}
			current, ok := best[col]
			if !ok || distance < current.distance {
				best[col] = candidate{distance: distance, style: style}
			}
		}
	}
	styles := make(map[string]string, len(best))
	for col, candidate := range best {
		styles[col] = candidate.style
	}
	return styles
}

func exactCellStyle(doc []byte, rows []xlsxRowSpan, ref string) (string, bool, error) {
	col, rowNum, err := splitCellRef(ref)
	if err != nil {
		return "", false, err
	}
	want := col + strconv.Itoa(rowNum)
	for _, row := range rows {
		if row.num != rowNum {
			continue
		}
		rowBytes := doc[row.start:row.end]
		cells, err := scanCells(rowBytes)
		if err != nil {
			return "", false, err
		}
		for _, cell := range cells {
			if strings.EqualFold(cell.ref, want) {
				return cellStyleAttr(rowBytes[cell.start:cell.end]), true, nil
			}
		}
		return "", false, nil
	}
	return "", false, nil
}

func columnStylesFromRow(doc []byte, rows []xlsxRowSpan, rowNum int) (map[string]string, bool, error) {
	for _, row := range rows {
		if row.num != rowNum {
			continue
		}
		rowBytes := doc[row.start:row.end]
		cells, err := scanCells(rowBytes)
		if err != nil {
			return nil, false, err
		}
		styles := make(map[string]string, len(cells))
		for _, cell := range cells {
			m := cellRefRe.FindStringSubmatch(cell.ref)
			if m != nil {
				styles[strings.ToUpper(m[1])] = cellStyleAttr(rowBytes[cell.start:cell.end])
			}
		}
		return styles, true, nil
	}
	return nil, false, nil
}

var cellAttrRe = regexp.MustCompile(`r="([A-Za-z]{1,3}[0-9]+)"`)

// scanCells locates the <c> elements inside one row's bytes.
func scanCells(row []byte) ([]xlsxCellSpan, error) {
	var cells []xlsxCellSpan
	pos := 0
	for {
		i := bytes.Index(row[pos:], []byte("<c"))
		if i < 0 {
			return cells, nil
		}
		start := pos + i
		after := start + 2
		if after < len(row) && row[after] != ' ' && row[after] != '>' && row[after] != '/' {
			pos = after
			continue
		}
		gt := bytes.IndexByte(row[start:], '>')
		if gt < 0 {
			return nil, fmt.Errorf("malformed sheet xml: unclosed <c>")
		}
		openEnd := start + gt + 1
		var span xlsxCellSpan
		span.start = start
		if row[start+gt-1] == '/' {
			span.end = openEnd
		} else {
			j := bytes.Index(row[openEnd:], []byte("</c>"))
			if j < 0 {
				return nil, fmt.Errorf("malformed sheet xml: unclosed <c>")
			}
			span.end = openEnd + j + len("</c>")
		}
		if m := cellAttrRe.FindSubmatch(row[start:openEnd]); m != nil {
			span.ref = strings.ToUpper(string(m[1]))
		}
		cells = append(cells, span)
		pos = span.end
	}
}

// xlsxAppendRow appends one row after the last used row.
// Returns the new doc and the new row's 1-based number.
func xlsxAppendRow(doc []byte, values []json.RawMessage) ([]byte, int, error) {
	return xlsxAppendRowWithStyleRow(doc, values, 0)
}

func xlsxAppendRowWithStyleRow(doc []byte, values []json.RawMessage, styleFromRow int) ([]byte, int, error) {
	if styleFromRow < 0 {
		return nil, 0, fmt.Errorf("style_from_row must be a positive row number")
	}
	doc, end, err := sheetDataInsertPoint(doc)
	if err != nil {
		return nil, 0, err
	}
	rows, err := scanRows(doc)
	if err != nil {
		return nil, 0, err
	}
	rowNum := 1
	for _, r := range rows {
		if r.num >= rowNum {
			rowNum = r.num + 1
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, `<row r="%d">`, rowNum)
	cols := make([]string, len(values))
	for i := range values {
		cols[i] = indexToCol(i + 1)
	}
	styles := nearestColumnStyles(doc, rows, rowNum, cols)
	if styleFromRow > 0 {
		var found bool
		styles, found, err = columnStylesFromRow(doc, rows, styleFromRow)
		if err != nil {
			return nil, 0, fmt.Errorf("style_from_row: %w", err)
		}
		if !found {
			return nil, 0, fmt.Errorf("style_from_row %d does not exist", styleFromRow)
		}
	}
	for i, v := range values {
		col := cols[i]
		ref := col + strconv.Itoa(rowNum)
		sb.WriteString(applyCellStyle(buildCellXML(ref, v), styles[col]))
	}
	sb.WriteString("</row>")
	return spliceBytes(doc, end, end, []byte(sb.String())), rowNum, nil
}

// spliceBytes returns doc with doc[from:to] replaced by ins.
func spliceBytes(doc []byte, from, to int, ins []byte) []byte {
	out := make([]byte, 0, len(doc)-(to-from)+len(ins))
	out = append(out, doc[:from]...)
	out = append(out, ins...)
	out = append(out, doc[to:]...)
	return out
}
