package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/tools/core"
)

func TestSavedToolOutputStringBindExactBytes(t *testing.T) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	samples := []string{"", "a\x00b", "Żółć 😀 日本語 العربية", string(all), string(bytes.Repeat(all, 48)), strings.Repeat("large bytes\n", 10000)}
	s := openTestStore(t)
	session, err := s.Create("synthetic-workspace", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, session.ID)
	for i, text := range samples {
		handle := fmt.Sprintf("out_bytes_%d", i)
		if err := w.SaveToolOutput(context.Background(), handle, text); err != nil {
			t.Fatal(err)
		}
		got, err := w.ReadToolOutput(context.Background(), handle)
		if err != nil || got != text {
			t.Fatalf("bytes %d lost: %v", i, err)
		}
		var kind string
		var size, meta int
		var saved []byte
		if err := s.db.QueryRow("SELECT typeof(content),length(content),bytes,content FROM tool_outputs WHERE handle=?", handle).Scan(&kind, &size, &meta, &saved); err != nil {
			t.Fatal(err)
		}
		if kind != "blob" || size != len(text) || meta != len(text) || !bytes.Equal(saved, []byte(text)) {
			t.Fatalf("blob invariant changed for sample %d: kind=%s,len=%d/meta=%d", i, kind, size, meta)
		}
		if err := w.SaveToolOutput(context.Background(), handle, "replacement"); err == nil {
			t.Fatal("immutable handle replaced")
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.SaveToolOutput(cancelled, "out_canceled_bind", "data"); err == nil {
		t.Fatal("cancelled save accepted")
	}
	if _, err := w.ReadToolOutput(context.Background(), "out_canceled_bind"); err == nil {
		t.Fatal("cancellation left durable row")
	}
	// Argument validation is intentionally prior to context cancellation.
	if err := w.SaveToolOutput(cancelled, "", "data"); err == nil || err.Error() != "invalid tool output or size limit exceeded" {
		t.Fatalf("error priority changed: %v", err)
	}
}

func TestSavedToolOutputPublicPipelineEvidence(t *testing.T) {
	s := openTestStore(t)
	session, err := s.Create("synthetic-workspace", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, session.ID)
	ctx := core.WithOutputPersistence(context.Background(), w)
	for _, failure := range []bool{false, true} {
		text := strings.Repeat("diagnostic Żółć \x00 newline\n", 1500)
		result := core.Result{Text: text}
		if failure {
			result.Err = fmt.Errorf("fixture command failed")
		}
		out := core.NewOutputStore()
		preview := out.ModelContentContext(ctx, "fixture_tool", result)
		marker := "handle="
		pos := strings.Index(preview, marker)
		if pos < 0 {
			t.Fatal("output was not retained")
		}
		handle := preview[pos+len(marker) : pos+len(marker)+36]
		persisted, err := w.ReadToolOutput(context.Background(), handle)
		if err != nil || persisted != text {
			t.Fatalf("durable evidence changed: %v", err)
		}
		// Fresh store + read_output resolves original bytes; UI text is untouched.
		fresh := core.NewOutputStore()
		reader := fresh.ReadOutputTool()
		raw := []byte(fmt.Sprintf("{\"handle\":%q,\"offset\":0,\"limit\":8192}", handle))
		got, err := reader.Fn(ctx, raw)
		if err != nil || got.Err != nil || !strings.Contains(got.Text, text[:4096]) {
			t.Fatalf("fresh read_output failed: %v/%v", err, got.Err)
		}
		digest := sha256.Sum256([]byte(persisted))
		if digest != sha256.Sum256([]byte(text)) {
			t.Fatal("persisted hash changed")
		}
	}
}

func BenchmarkSavedOutputBindPublic(b *testing.B) {
	for _, size := range []int{12311, 34404, 128 << 10, 1 << 20, 4382834} {
		b.Run(fmt.Sprintf("size-%d", size), func(b *testing.B) {
			store, err := OpenStore(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			sess, err := store.Create("synthetic-workspace", "fixture", "")
			if err != nil {
				b.Fatal(err)
			}
			w := NewWriter(store, sess.ID)
			text := strings.Repeat("x", size)
			if err := w.SaveToolOutput(context.Background(), "out_prime", text); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := w.SaveToolOutput(context.Background(), fmt.Sprintf("out_%d", i), text); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSavedOutputModelContentPublic(b *testing.B) {
	for _, size := range []int{512, 12311, 34404, 128 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("size-%d", size), func(b *testing.B) {
			store, err := OpenStore(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			sess, err := store.Create("synthetic-workspace", "fixture", "")
			if err != nil {
				b.Fatal(err)
			}
			w := NewWriter(store, sess.ID)
			ctx := core.WithOutputPersistence(context.Background(), w)
			output := core.NewOutputStore()
			text := strings.Repeat("x", size)
			result := core.Result{Text: text}
			output.ModelContentContext(ctx, "fixture_tool", result)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				model := output.ModelContentContext(ctx, "fixture_tool", result)
				if len(model) == 0 {
					b.Fatal("empty model output")
				}
			}
		})
	}
}

func TestSavedToolOutputDatabaseEncodingRawBytes(t *testing.T) {
	for _, encoding := range []string{"UTF-8", "UTF-16le", "UTF-16be"} {
		t.Run(encoding, func(t *testing.T) {
			home := t.TempDir()
			db, err := sql.Open("sqlite", filepath.Join(home, "sessions.db"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("PRAGMA encoding='" + encoding + "'; CREATE TABLE encoding_fixture (id INTEGER PRIMARY KEY)"); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			s, err := OpenStore(home)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			sess, err := s.Create("synthetic-workspace", "fixture", "")
			if err != nil {
				t.Fatal(err)
			}
			w := NewWriter(s, sess.ID)
			all := make([]byte, 256)
			for i := range all {
				all[i] = byte(i)
			}
			for i, text := range []string{"", "a\x00b", "Żółć 日本語", string(all)} {
				h := []string{"out_empty", "out_nul", "out_utf8", "out_invalid"}[i]
				if err = w.SaveToolOutput(context.Background(), h, text); err != nil {
					t.Fatal(err)
				}
				var kind string
				var n, m int
				var got []byte
				if err = s.db.QueryRow("SELECT typeof(content),length(content),bytes,content FROM tool_outputs WHERE handle=?", h).Scan(&kind, &n, &m, &got); err != nil {
					t.Fatal(err)
				}
				if kind != "blob" || n != len(text) || m != len(text) || !bytes.Equal(got, []byte(text)) {
					t.Fatalf("persisted raw bytes changed: kind=%s, len=%d metadata=%d original=%d", kind, n, m, len(text))
				}
			}
		})
	}
}

var saveBindFaultID atomic.Uint64

type saveBindFaultDriver struct{ state *saveBindFaultConn }

func (d saveBindFaultDriver) Open(string) (driver.Conn, error) { return d.state, nil }

type saveBindFaultConn struct {
	encoding      string
	queryErr      error
	queries       int
	inserts       []any
	insertQueries []string
}

func (c *saveBindFaultConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fixture prepared statements unsupported")
}
func (c *saveBindFaultConn) Close() error              { return nil }
func (c *saveBindFaultConn) Begin() (driver.Tx, error) { return saveBindFaultTx{}, nil }
func (c *saveBindFaultConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return saveBindFaultTx{}, nil
}
func (c *saveBindFaultConn) QueryContext(ctx context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q != "PRAGMA encoding" {
		return nil, fmt.Errorf("unexpected query %q", q)
	}
	c.queries++
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	return &saveBindFaultRows{encoding: c.encoding}, nil
}
func (c *saveBindFaultConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.HasPrefix(q, "INSERT INTO tool_outputs") {
		c.inserts = append(c.inserts, args[2].Value)
		c.insertQueries = append(c.insertQueries, q)
	}
	return driver.RowsAffected(1), nil
}

type saveBindFaultRows struct {
	encoding string
	done     bool
}

func (r *saveBindFaultRows) Columns() []string { return []string{"encoding"} }
func (r *saveBindFaultRows) Close() error      { return nil }
func (r *saveBindFaultRows) Next(v []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	v[0] = r.encoding
	return nil
}

type saveBindFaultTx struct{}

func (saveBindFaultTx) Commit() error   { return nil }
func (saveBindFaultTx) Rollback() error { return nil }
func TestSavedToolOutputLazyEncodingFallback(t *testing.T) {
	for _, tc := range []struct {
		name, encoding string
		queryErr       error
		stringBind     bool
	}{{"utf8", "UTF-8", nil, true}, {"utf16", "UTF-16le", nil, false}, {"unknown", "future-encoding", nil, false}, {"failed", "", errors.New("fixture encoding read failed"), false}} {
		t.Run(tc.name, func(t *testing.T) {
			state := &saveBindFaultConn{encoding: tc.encoding, queryErr: tc.queryErr}
			name := fmt.Sprintf("savebind_fault_%d", saveBindFaultID.Add(1))
			sql.Register(name, saveBindFaultDriver{state})
			db, err := sql.Open(name, "")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			w := NewWriter(&Store{db: db}, "fixture")
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			if err := w.SaveToolOutput(cancelled, "", "data"); err == nil || err.Error() != "invalid tool output or size limit exceeded" {
				t.Fatalf("validation priority changed: %v", err)
			}
			if err := w.SaveToolOutput(cancelled, "out_cancel", "data"); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel priority changed: %v", err)
			}
			if state.queries != 0 {
				t.Fatal("failed validation/BeginTx consumed encoding lookup")
			}
			for i := 0; i < 2; i++ {
				if err := w.SaveToolOutput(context.Background(), fmt.Sprintf("out_%d", i), "a\x00b"); err != nil {
					t.Fatal(err)
				}
			}
			if state.queries != 1 || len(state.inserts) != 2 {
				t.Fatalf("lookup repeated or evidence missing: queries=%d, inserts=%d", state.queries, len(state.inserts))
			}
			for i, content := range state.inserts {
				_, stringBind := content.(string)
				if stringBind != tc.stringBind || strings.Contains(state.insertQueries[i], "CAST") != tc.stringBind {
					t.Fatalf("unsafe binding selected: type=%T, query=%s", content, state.insertQueries[i])
				}
				if !stringBind {
					b, ok := content.([]byte)
					if !ok || string(b) != "a\x00b" {
						t.Fatal("byte fallback changed")
					}
				}
			}
		})
	}
}

func BenchmarkSavedOutputOpenExistingPublic(b *testing.B) {
	home := b.TempDir()
	s, err := OpenStore(home)
	if err != nil {
		b.Fatal(err)
	}
	if err = s.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, err := OpenStore(home)
		if err != nil {
			b.Fatal(err)
		}
		if err = s.Close(); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(reflect.TypeOf(Store{}).Size()), "store-bytes")
}

func BenchmarkSavedOutputFirstSavePublic(b *testing.B) {
	for _, size := range []int{12311, 1 << 20} {
		b.Run(fmt.Sprintf("size-%d", size), func(b *testing.B) {
			text := strings.Repeat("x", size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				store, err := OpenStore(b.TempDir())
				if err != nil {
					b.Fatal(err)
				}
				sess, err := store.Create("synthetic-workspace", "fixture", "")
				if err != nil {
					b.Fatal(err)
				}
				w := NewWriter(store, sess.ID)
				b.StartTimer()
				if err := w.SaveToolOutput(context.Background(), "out_first", text); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				if err := store.Close(); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
		})
	}
}
