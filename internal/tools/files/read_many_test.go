package files

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadMany_ArrayPreservesOrderAndPartialErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a1\na2\na3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b1\nb2\nb3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadMany(dir)
	res, err := tool.execute(context.Background(), []byte(`{"reads":[{"file":"a.txt","from":1,"to":2},{"file":"missing.txt","from":1,"to":2},{"file":"b.txt","from":2,"to":3}]}`))
	if err != nil || res.Err != nil {
		t.Fatalf("execute err=%v result.err=%v", err, res.Err)
	}
	for _, want := range []string{"== [1] a.txt:1-2 ==", "a1", "== [2] missing.txt:1-2 ==", "error: not_found", "== [3] b.txt:2-3 ==", "b2", "[read_many: 2 ok, 1 failed]"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("output missing %q:\n%s", want, res.Text)
		}
	}
	if strings.Index(res.Text, "a.txt") > strings.Index(res.Text, "b.txt") {
		t.Error("results did not preserve request order")
	}
}

func TestReadManySpecIsCompactAndReadOnly(t *testing.T) {
	spec := NewReadMany(".").Spec()
	if !spec.ReadOnly {
		t.Fatal("read_many must be certified read-only")
	}
	if len(spec.Schema) > 400 {
		t.Fatalf("read_many schema = %d bytes, want <= 400", len(spec.Schema))
	}
	if !strings.Contains(spec.Description, "native") || !strings.Contains(spec.Description, "sentinel") {
		t.Fatalf("description must explain cross-backend format: %q", spec.Description)
	}
}

func TestReadMany_SentinelShorthand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("four\nfive\nsix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := NewReadMany(dir).execute(context.Background(), []byte(`{"reads":"a.go:1-2 | b.go:2-3"}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	for _, want := range []string{"one", "two", "five", "six"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("missing %q: %s", want, res.Text)
		}
	}
}

func TestReadMany_BareFilesUseBoundedDefaultRange(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"a.go": "alpha\n", "b.go": "bravo\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, _ := NewReadMany(dir).execute(context.Background(), []byte(`{"reads":"a.go | b.go"}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	for _, want := range []string{"a.go:1-300", "alpha", "b.go:1-300", "bravo", "[read_many: 2 ok, 0 failed]"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("bare-file output missing %q:\n%s", want, res.Text)
		}
	}
}

func TestReadMany_ArrayFileOnlyUsesBoundedDefaultRange(t *testing.T) {
	got, err := decodeReadManyRequests([]byte(`[{"file":"a.go"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].From != 1 || got[0].To != maxReadManyRange {
		t.Fatalf("decoded defaults = %+v", got)
	}
}

func TestReadMany_GlobExpandsDeterministically(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"b.go": "bravo\n", "a.go": "alpha\n", "skip.txt": "skip\n"} {
		if err := os.WriteFile(filepath.Join(dir, "internal", name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, _ := NewReadMany(dir).execute(context.Background(), []byte(`{"reads":"internal/*.go:1-1"}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	for _, want := range []string{"== [1] internal/a.go:1-1 ==", "alpha", "== [2] internal/b.go:1-1 ==", "bravo", "[read_many: 2 ok, 0 failed]"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("glob output missing %q:\n%s", want, res.Text)
		}
	}
	if strings.Index(res.Text, "internal/a.go") > strings.Index(res.Text, "internal/b.go") {
		t.Fatalf("glob order is not deterministic: %s", res.Text)
	}
}

func TestReadMany_GlobNoMatchIsPartialError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := NewReadMany(dir).execute(context.Background(), []byte(`{"reads":"ok.txt:1-1 | missing/*.go:1-2"}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	for _, want := range []string{"ok", "glob_no_matches missing/*.go", "[read_many: 1 ok, 1 failed]"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("partial glob output missing %q:\n%s", want, res.Text)
		}
	}
}

func TestReadMany_GlobExpansionHonorsGlobalCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxReadManyRequests+1; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d.go", i)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, _ := NewReadMany(dir).execute(context.Background(), []byte(`{"reads":"*.go:1-1"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "glob expansion produced") {
		t.Fatalf("want expansion cap error, got %+v", res)
	}
}

func TestDecodeReadManyShorthand_WindowsDrive(t *testing.T) {
	got, err := decodeReadManyRequests([]byte(`"C:\\\\repo\\\\main.go:10-20 | D:\\\\x.go:1-2"`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].From != 10 || got[0].To != 20 || !strings.HasPrefix(got[0].File, `C:\`) {
		t.Fatalf("decoded = %+v", got)
	}
}

func TestReadMany_GlobalOutputBound(t *testing.T) {
	dir := t.TempDir()
	line := strings.Repeat("x", 1900)
	content := strings.Repeat(line+"\n", 300)
	var reads []string
	for i := 0; i < maxReadManyRequests; i++ {
		name := fmt.Sprintf("%d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		reads = append(reads, fmt.Sprintf(`{"file":%q,"from":1,"to":300}`, name))
	}
	args := `{"reads":[` + strings.Join(reads, ",") + `]}`
	res, _ := NewReadMany(dir).execute(context.Background(), []byte(args))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if len(res.Text) > maxReadManyBytes+256 {
		t.Fatalf("output = %d bytes, want <= %d-ish", len(res.Text), maxReadManyBytes)
	}
	if !strings.Contains(res.Text, "omitted_bytes") {
		t.Error("bounded output missing explicit omission marker")
	}
}

func TestReadMany_HugeLineIsStreamedAndBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("ż", 3*1024*1024)+"\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _ := NewReadMany(dir).execute(context.Background(), []byte(`{"reads":"huge.txt:1-1"}`))
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if len(res.Text) > 4096 {
		t.Fatalf("single huge-line result = %d bytes, want bounded", len(res.Text))
	}
	if !strings.Contains(res.Text, "truncated") {
		t.Fatalf("huge line missing truncation marker: %s", res.Text)
	}
}
