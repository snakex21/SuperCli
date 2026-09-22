package office

import (
	"bytes"
	"fmt"
	"strings"
)

func (t *EditDocxTool) doInsertAfter(full string, p editDocxArgs) (Result, error) {
	if strings.TrimSpace(p.Text) == "" {
		err := fmt.Errorf("edit_docx insert_after: 'text' is required")
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	content, stats := buildDocxBlocksMatchingDocument(doc, p.Text, p.Style)
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
		return Result{Err: fmt.Errorf("edit_docx insert_after: %w", err)}, err
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would insert %d paragraph(s) and %d table(s) after %s in %s. Nothing was written.", stats.Paragraphs, stats.Tables, after, full)}, nil
	}
	backup, err := editZipEntryInPlace(full, docxDocumentEntry, newDoc)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Inserted %d paragraph(s) and %d table(s) after %s in %s. Backup of the original saved as %s.", stats.Paragraphs, stats.Tables, after, full, backup)}, nil
}

func (t *EditDocxTool) doInsertTable(full string, p editDocxArgs) (Result, error) {
	rows, err := parseDocxTableText(p.Text)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_table: %w", err)}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	header := p.HeaderRow == nil || *p.HeaderRow
	tableXML := buildDocxTableXML(rows, header)
	after := strings.TrimSpace(p.After)
	var newDoc []byte
	if after == "" || strings.EqualFold(after, "end") {
		newDoc, err = docxInsertBeforeBodyEnd(doc, tableXML)
		after = "end"
	} else {
		var insertAt int
		insertAt, err = findDocxInsertionPoint(doc, after)
		if err == nil {
			newDoc = spliceBytes(doc, insertAt, insertAt, tableXML)
		}
	}
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx insert_table: %w", err)}, err
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would insert a %d-row x %d-column Word table after %s in %s. Nothing was written.", len(rows), len(rows[0]), after, full)}, nil
	}
	backup, err := editZipEntryInPlace(full, docxDocumentEntry, newDoc)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Inserted a %d-row x %d-column Word table after %s in %s. Backup of the original saved as %s.", len(rows), len(rows[0]), after, full, backup)}, nil
}

func (t *EditDocxTool) doTableAddRow(full string, p editDocxArgs) (Result, error) {
	if strings.TrimSpace(p.Source) == "" {
		err := fmt.Errorf("edit_docx table_add_row: 'source' is required (for example /body/tbl[1])")
		return Result{Err: err}, err
	}
	rows, err := parseDocxTableText(p.Text)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx table_add_row: %w", err)}, err
	}
	if p.Action == "table_add_row" && len(rows) != 1 {
		err := fmt.Errorf("edit_docx table_add_row: expected exactly one row, got %d; use table_add_rows for a batch", len(rows))
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	table, err := findDocxTableLocation(doc, p.Source)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx table_add_row: %w", err)}, err
	}
	if len(table.rows) == 0 {
		err := fmt.Errorf("edit_docx table_add_row: %s contains no rows", p.Source)
		return Result{Err: err}, err
	}
	existingCols, err := countTopLevelCells(doc[table.rows[0].start:table.rows[0].end])
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx table_add_row: count columns: %w", err)}, err
	}
	for i, row := range rows {
		if len(row) != existingCols {
			err := fmt.Errorf("edit_docx table_add_row: row %d has %d cells but %s has %d columns", i+1, len(row), p.Source, existingCols)
			return Result{Err: err}, err
		}
	}
	tableFrag := doc[table.start:table.end]
	closeAt := bytes.LastIndex(tableFrag, []byte("</w:tbl>"))
	if closeAt < 0 {
		err := fmt.Errorf("edit_docx table_add_row: malformed %s: missing </w:tbl>", p.Source)
		return Result{Err: err}, err
	}
	var rowXML bytes.Buffer
	for i, row := range rows {
		rowXML.Write(buildDocxTableRowXML(row, false, len(table.rows)+i, 5000/existingCols))
	}
	insertAt := table.start + closeAt
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would add %d row(s) with %d cells each to %s in %s. Nothing was written.", len(rows), existingCols, p.Source, full)}, nil
	}
	newDoc := spliceBytes(doc, insertAt, insertAt, rowXML.Bytes())
	backup, err := editZipEntryInPlace(full, docxDocumentEntry, newDoc)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Added %d row(s) with %d cells each to %s in %s. Backup of the original saved as %s.", len(rows), existingCols, p.Source, full, backup)}, nil
}

func (t *EditDocxTool) doDeleteAt(full string, p editDocxArgs) (Result, error) {
	selector := strings.TrimSpace(p.Source)
	if selector == "" {
		err := fmt.Errorf("edit_docx delete_at: 'source' is required")
		return Result{Err: err}, err
	}
	doc, err := t.loadDocumentXML(full)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	var target docxElementLocation
	switch {
	case docxTableSelectorRe.MatchString(selector):
		table, findErr := findDocxTableLocation(doc, selector)
		if findErr != nil {
			return Result{Err: fmt.Errorf("edit_docx delete_at: %w", findErr)}, findErr
		}
		target = table.docxElementLocation
	case docxRowSelectorRe.MatchString(selector):
		match := docxRowSelectorRe.FindStringSubmatch(selector)
		tableSelector := fmt.Sprintf("/body/tbl[%s]", match[1])
		table, findErr := findDocxTableLocation(doc, tableSelector)
		if findErr != nil {
			return Result{Err: fmt.Errorf("edit_docx delete_at: %w", findErr)}, findErr
		}
		if len(table.rows) == 1 {
			err := fmt.Errorf("edit_docx delete_at: cannot delete the only row in %s; delete the whole table instead", tableSelector)
			return Result{Err: err}, err
		}
		for _, row := range table.rows {
			if strings.EqualFold(row.selector, selector) {
				target = row
				break
			}
		}
		if target.end == 0 {
			err := fmt.Errorf("edit_docx delete_at: row selector %q was not found", selector)
			return Result{Err: err}, err
		}
	default:
		paragraph, findErr := findDocxParagraphLocation(doc, selector)
		if findErr != nil {
			return Result{Err: fmt.Errorf("edit_docx delete_at: %w", findErr)}, findErr
		}
		if docxOffsetInsideTable(doc, paragraph.start) {
			err := fmt.Errorf("edit_docx delete_at: refusing to remove a table-cell paragraph because Word cells require a paragraph; use replace_at with empty text")
			return Result{Err: err}, err
		}
		target = docxElementLocation{selector: paragraph.selector, start: paragraph.start, end: paragraph.end}
	}
	if p.DryRun {
		return Result{Text: fmt.Sprintf("Preview only: would delete %s from %s. Nothing was written.", selector, full)}, nil
	}
	newDoc := spliceBytes(doc, target.start, target.end, nil)
	backup, err := editZipEntryInPlace(full, docxDocumentEntry, newDoc)
	if err != nil {
		return Result{Err: fmt.Errorf("edit_docx: %w", err)}, err
	}
	return Result{Text: fmt.Sprintf("Deleted %s from %s. Backup of the original saved as %s.", selector, full, backup)}, nil
}
