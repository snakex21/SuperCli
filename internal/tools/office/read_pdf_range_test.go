package office

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ledongthuc/pdf"
)

func pdfPageOracle(t *testing.T, p string, page int) string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := pdf.NewReader(f, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	text, err := NewReadPdf(filepath.Dir(p), 0).renderPage(reader.Page(page))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("--- Page %d ---\n%s", page, text)
}

func TestReadPdf_StartPageReadsRequestedPage(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPdfPages(t, dir, "twenty.pdf", "Requested page content", 20)
	tool := NewReadPdf(dir, 0)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"twenty.pdf","start_page":17,"max_pages":1}`))
	if err != nil || res.Err != nil {
		t.Fatalf("range read: %+v err=%v", res, err)
	}
	oracle := pdfPageOracle(t, p, 17)
	if !strings.HasPrefix(res.Text, oracle) || strings.Contains(res.Text, "--- Page 1 ---") || strings.Contains(res.Text, "--- Page 18 ---") {
		t.Fatalf("wanted exact page17 oracle %q, got %q", oracle, res.Text)
	}
	if !strings.Contains(res.Text, "17-17 of 20") {
		t.Fatalf("range lacks actual document bounds: %q", res.Text)
	}
}

func TestReadPdf_DefaultFullReadIsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPdfPages(t, dir, "three.pdf", "Default full read", 3)
	want := strings.Join([]string{pdfPageOracle(t, p, 1), pdfPageOracle(t, p, 2), pdfPageOracle(t, p, 3)}, "\n\n")
	res, err := NewReadPdf(dir, 0).Execute(context.Background(), json.RawMessage(`{"path":"three.pdf"}`))
	if err != nil || res.Err != nil || res.Text != want {
		t.Fatalf("default changed: text=%q want=%q Err=%v err=%v", res.Text, want, res.Err, err)
	}
}

func TestReadPdf_StartPageOutsideDocumentIsError(t *testing.T) {
	dir := t.TempDir()
	writeTestPdfPages(t, dir, "three.pdf", "Page", 3)
	for _, start := range []int{0, -1, 4, int(^uint(0) >> 1)} {
		args, _ := json.Marshal(map[string]any{"path": "three.pdf", "start_page": start, "max_pages": 1})
		res, err := NewReadPdf(dir, 0).Execute(context.Background(), args)
		if err == nil || res.Err == nil {
			t.Errorf("start_page=%d became success: %+v err=%v", start, res, err)
		}
	}
}

func TestReadPdf_StartPageCountAndEOFOracle(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPdfPages(t, dir, "three.pdf", "Count range", 3)
	for _, tc := range []struct {
		start, max int
		pages      []int
	}{
		{2, 1, []int{2}}, {2, int(^uint(0) >> 1), []int{2, 3}}, {3, 1000, []int{3}}, {2, 0, []int{2, 3}}, {2, -1, []int{2, 3}},
	} {
		args, _ := json.Marshal(map[string]any{"path": "three.pdf", "start_page": tc.start, "max_pages": tc.max})
		res, err := NewReadPdf(dir, 0).Execute(context.Background(), args)
		oracle := make([]string, 0, len(tc.pages))
		for _, page := range tc.pages {
			oracle = append(oracle, pdfPageOracle(t, p, page))
		}
		want := strings.Join(oracle, "\n\n") + fmt.Sprintf("\n\n[Showing pages %d-%d of 3]", tc.start, tc.pages[len(tc.pages)-1])
		if err != nil || res.Err != nil || res.Text != want {
			t.Fatalf("start=%d count=%d: text=%q want=%q Err=%v err=%v", tc.start, tc.max, res.Text, want, res.Err, err)
		}
	}
}

func TestReadPdf_RangeKeepsConfiguredAndOutputBounds(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPdfPages(t, dir, "twenty.pdf", "Read with bounds", 20)
	tool := NewReadPdf(dir, 0)
	tool.MaxPages = 2
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"twenty.pdf","start_page":17}`))
	if err != nil || res.Err != nil || !strings.Contains(res.Text, "--- Page 17 ---") || !strings.Contains(res.Text, "--- Page 18 ---") || strings.Contains(res.Text, "--- Page 19 ---") || !strings.Contains(res.Text, "17-18 of 20") {
		t.Fatalf("configured page limit: %+v err=%v", res, err)
	}
	tool.MaxOutputBytes = int64(len(pdfPageOracle(t, p, 17)))
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"path":"twenty.pdf","start_page":17,"max_pages":1}`))
	if err == nil || res.Err == nil || !strings.Contains(res.Err.Error(), "rendered text exceeds") {
		t.Fatalf("range notice exceeded output bound: %+v err=%v", res, err)
	}
}

type cancelingPDFReaderAt struct {
	source    io.ReaderAt
	cancel    context.CancelFunc
	armed     bool
	triggered bool
}

func (r *cancelingPDFReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.source.ReadAt(p, off)
	if r.armed {
		r.armed = false
		r.triggered = true
		r.cancel()
	}
	return n, err
}

func TestReadPdf_CancelDuringPageExtractionStopsRange(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPdfPages(t, dir, "twenty.pdf", strings.Repeat("cancel during extraction ", 200), 20)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &cancelingPDFReaderAt{source: f, cancel: cancel}
	reader, err := pdf.NewReader(source, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.NumPage()
	source.armed = true
	text, err := NewReadPdf(dir, 0).renderPages(ctx, reader, 10, 2)
	if !source.triggered {
		t.Fatal("fixture did not cancel during PDF page I/O")
	}
	if err != context.Canceled || text != "" {
		t.Fatalf("cancel ignored or partial success: text=%q err=%v", text, err)
	}
}

func TestReadPdf_PreCanceledRenderAndInvalidArgs(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPdfPages(t, dir, "one.pdf", "Unchanged workbook", 1)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewReadPdf(dir, 0)
	for _, args := range []string{`{"path":"one.pdf","start_page":"1"}`, `{"path":"one.pdf","start_page":1.5}`, `{"path":"../outside.pdf","start_page":1}`} {
		res, err := tool.Execute(context.Background(), json.RawMessage(args))
		if res.Err == nil {
			t.Errorf("invalid input became success: %+v err=%v", res, err)
		}
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"one.pdf","start_page":null}`))
	if err != nil || res.Err != nil || res.Text != pdfPageOracle(t, p, 1) {
		t.Fatalf("null optional field changed default: %+v err=%v", res, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.renderPages(ctx, nil, 1, 1); err != context.Canceled {
		t.Fatalf("canceled render did not stop before reader access: %v", err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("read_pdf mutated its input")
	}
}
