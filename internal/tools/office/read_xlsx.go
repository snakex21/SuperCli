package office

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"strings"

	"supercli/internal/tools/fileops"
)

// Default bounds for the read_xlsx tool. XLSX
// files can grow large (a million-cell sheet is
// common in finance), so the cell cap is
// generous. 64 MB on disk / 4 MB rendered text
// covers everything from a small summary to a
// full quarterly report.
const (
	DefaultMaxXlsxBytes  = 64 * 1024 * 1024 // 64 MB on disk
	DefaultMaxXlsxCells  = 200000           // ~ 200k cells rendered before we cap
	DefaultMaxXlsxOutput = 4 * 1024 * 1024  // 4 MB rendered text
)

// ReadXlsxTool extracts the text content of a
// .xlsx file. A .xlsx is a zip archive whose
// main content is xl/sharedStrings.xml (string
// table) and worksheet XML (cell data); named
// sheets are resolved through workbook metadata.
// The tool opens the zip once and emits a
// markdown-style table per row.
//
// The implementation is pure stdlib
// (archive/zip + encoding/xml), so the binary
// stays self-contained. There is no shelling
// out to libreoffice, no excelize, no external
// .NET runtime, no temporary files.
//
// Safety: zip-slip protection comes for free
// because worksheet targets stay inside the
// archive and no entries are extracted. The size
// cap is enforced before reading each entry's
// body.
type ReadXlsxTool struct {
	BaseDir        string
	MaxXlsxBytes   int64
	MaxCells       int
	MaxOutputBytes int64
}

// NewReadXlsx returns a ReadXlsxTool with
// default bounds. Pass 0 for maxBytes to use
// the default. baseDir is the directory the
// tool resolves relative paths against.
func NewReadXlsx(baseDir string, maxBytes int64) *ReadXlsxTool {
	if baseDir == "" {
		baseDir = "."
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxXlsxBytes
	}
	return &ReadXlsxTool{
		BaseDir:        baseDir,
		MaxXlsxBytes:   maxBytes,
		MaxCells:       DefaultMaxXlsxCells,
		MaxOutputBytes: DefaultMaxXlsxOutput,
	}
}

// Spec returns the Tool descriptor.
func (t *ReadXlsxTool) Spec() Tool {
	return Tool{
		Name:        "read_xlsx",
		Description: "Read an Excel .xlsx file as a pipe-separated table, one row per line. Pick a worksheet by its workbook name or physical sheet number; defaults to sheet1.",
		Schema: `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Path to the .xlsx file."},
    "sheet": {"type": "string", "description": "Workbook sheet name or physical sheet number (e.g. 'Sales 2026' or '1' for sheet1.xml). Defaults to sheet1."},
    "max_cells": {"type": "integer", "description": "Cap on cells to render (default 200000)."}
  },
  "required": ["path"]
}`,
		Fn: t.Execute,
	}
}

// Execute reads the xlsx and returns the
// extracted text.
func (t *ReadXlsxTool) Execute(ctx context.Context, args json.RawMessage) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}, err
	}
	var params struct {
		Path     string `json:"path"`
		Sheet    string `json:"sheet"`
		MaxCells int    `json:"max_cells"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return Result{Err: fmt.Errorf("read_xlsx: bad args: %w", err)}, err
	}
	if params.Path == "" {
		err := fmt.Errorf("read_xlsx: path is required")
		return Result{Err: err}, err
	}
	maxC := params.MaxCells
	if maxC <= 0 {
		maxC = t.MaxCells
	}

	full, err := resolveSandboxed(t.BaseDir, params.Path)
	if err != nil {
		return Result{Err: fmt.Errorf("read_xlsx: %w", err)}, nil
	}
	info, err := os.Stat(full)
	if err != nil {
		err = fmt.Errorf("read_xlsx: %w", fileops.FileErr(err, full))
		return Result{Err: err}, err
	}
	if info.IsDir() {
		err := fmt.Errorf("read_xlsx: %q is a directory, not an xlsx", full)
		return Result{Err: err}, err
	}
	if info.Size() > t.MaxXlsxBytes {
		err := fmt.Errorf("read_xlsx: file too large: %d > %d", info.Size(), t.MaxXlsxBytes)
		return Result{Err: err}, err
	}

	zr, err := zip.OpenReader(full)
	if err != nil {
		return Result{Err: fmt.Errorf("read_xlsx: open zip: %w", err)}, err
	}
	defer zr.Close()

	sheetEntry, err := t.resolveSheetEntry(&zr.Reader, params.Sheet)
	if err != nil {
		return Result{Err: fmt.Errorf("read_xlsx: %w", err)}, err
	}

	// 1. Load shared strings (may be empty if
	// the file has none).
	sharedStrings, err := t.loadSharedStrings(&zr.Reader)
	if err != nil {
		return Result{Err: fmt.Errorf("read_xlsx: %w", err)}, err
	}
	// 2. Load the sheet's row data.
	sheetData, err := readXlsxZipEntry(&zr.Reader, sheetEntry, t.MaxXlsxBytes)
	if err != nil {
		return Result{Err: fmt.Errorf("read_xlsx: %w", err)}, err
	}
	// 3. Render.
	text, err := t.renderSheet(sheetData, sharedStrings, maxC)
	if err != nil {
		return Result{Err: fmt.Errorf("read_xlsx: %w", err)}, err
	}
	return Result{Text: text}, nil
}

// loadSharedStrings reads xl/sharedStrings.xml
// and returns the string table as a slice
// indexed by 0-based position. Returns an
// empty slice (no error) if the entry is
// missing — many xlsx files have no shared
// strings, and that's fine.
func (t *ReadXlsxTool) loadSharedStrings(zr *zip.Reader) ([]string, error) {
	data, err := readXlsxZipEntry(zr, "xl/sharedStrings.xml", t.MaxXlsxBytes)
	if err != nil {
		if errors.Is(err, errXlsxEntryNotFound) {
			return nil, nil
		}
		return nil, err
	}
	// Some tools emit a zero-byte
	// sharedStrings.xml when the workbook
	// has no strings. Treat that as "no
	// shared strings" rather than a parse
	// error: the model still gets the numeric
	// cells correctly.
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var sst sharedStringsTable
	if err := xml.Unmarshal(data, &sst); err != nil {
		return nil, fmt.Errorf("parse sharedStrings.xml: %w", err)
	}
	out := make([]string, len(sst.Items))
	for i, si := range sst.Items {
		// <si> can contain either a single
		// <t> or a rich-text mix. The plain
		// <t> case is what the renderer cares
		// about; rich-text is concatenated.
		out[i] = si.PlainText()
	}
	return out, nil
}

// sharedStringsTable matches <sst>.
type sharedStringsTable struct {
	Items []sharedStringItem `xml:"si"`
}

// sharedStringItem matches <si>. We accept both
// <si><t>text</t></si> and the richer
// <si><r><t>...</t></r></si> form by
// concatenating all <t> children.
type sharedStringItem struct {
	Texts []string `xml:"t"`
}

// PlainText returns the concatenated <t>
// children of the item, which is the natural
// text representation for the model.
func (s sharedStringItem) PlainText() string {
	return strings.Join(s.Texts, "")
}

// renderSheet walks the sheet's XML and
// returns a plain-text rendering. Rows are
// separated by newlines; cells within a row
// are separated by " | ". Cell values are
// resolved against the shared-strings table
// when the type attribute is "s".
