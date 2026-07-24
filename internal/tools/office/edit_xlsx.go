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
	release := fileops.LockMutationPaths(full)
	defer release()
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
