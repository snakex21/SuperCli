package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOutputStore_SmallResultUnchanged(t *testing.T) {
	s := NewOutputStore()
	if got := s.Compact("read_many", "small result"); got != "small result" {
		t.Fatalf("small result changed: %q", got)
	}
}

func TestOutputStore_LargeResultPreviewAndRead(t *testing.T) {
	s := NewOutputStore()
	original := "BEGIN\n" + strings.Repeat("0123456789", 2000) + "\nEND"
	preview := s.Compact("read_many", original)
	if len(preview) >= len(original)/2 {
		t.Fatalf("preview too large: %d vs %d", len(preview), len(original))
	}
	for _, want := range []string{"handle=out_000001", "BEGIN", "END", "read_output"} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview missing %q", want)
		}
	}

	tool := s.ReadOutputTool()
	res, err := tool.Fn(context.Background(), json.RawMessage(`{"handle":"out_000001","offset":4096,"limit":4096}`))
	if err != nil || res.Err != nil {
		t.Fatalf("read_output: result=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Text, "bytes 4096:8192") || !strings.Contains(res.Text, "[next offset: 8192]") {
		t.Fatalf("unexpected chunk metadata:\n%s", res.Text)
	}
}

func TestOutputStore_UTF8ChunkBoundaries(t *testing.T) {
	s := NewOutputStore()
	original := strings.Repeat("ż", outputInlineBytes)
	preview := s.Compact("read_many", original)
	if !utf8.ValidString(preview) {
		t.Fatal("preview is not valid UTF-8")
	}
	chunk, start, _, err := s.read("out_000001", 1, 101)
	if err != nil {
		t.Fatal(err)
	}
	if start != 2 || !utf8.ValidString(chunk) {
		t.Fatalf("invalid aligned chunk: start=%d valid=%v", start, utf8.ValidString(chunk))
	}
}

func TestRegistryEnsureReadOutput_Idempotent(t *testing.T) {
	r := NewRegistry()
	r.EnsureReadOutput()
	r.EnsureReadOutput()
	if r.Len() != 1 {
		t.Fatalf("Len=%d, want 1", r.Len())
	}
	if !r.IsVisible("read_output") {
		t.Fatal("read_output must be visible")
	}
}

var hintRe = regexp.MustCompile(`"offset":(\d+),"limit":(\d+)`)

// parseHint extracts the offset/limit a model would copy out of the paging
// hint. It fails the test when the hint is missing, so callers double as a
// check that a large result advertises paging at all.
func parseHint(t *testing.T, preview string) (offset, limit int) {
	t.Helper()
	m := hintRe.FindStringSubmatch(preview)
	if m == nil {
		t.Fatalf("no paging hint in preview:\n%s", preview)
	}
	offset, _ = strconv.Atoi(m[1])
	limit, _ = strconv.Atoi(m[2])
	return offset, limit
}

// walkOutput replays what a model does when it obeys the hint: read a chunk,
// follow the "[next offset: N]" footer with the same limit, repeat. It returns
// the number of read_output calls and the concatenated bytes.
func walkOutput(t *testing.T, s *OutputStore, handle string, offset, limit int) (calls int, got string) {
	t.Helper()
	tool := s.ReadOutputTool()
	nextRe := regexp.MustCompile(`\[next offset: (\d+)\]`)
	for i := 0; i < 64; i++ {
		args := fmt.Sprintf(`{"handle":%q,"offset":%d,"limit":%d}`, handle, offset, limit)
		res, err := tool.Fn(context.Background(), json.RawMessage(args))
		if err != nil || res.Err != nil {
			t.Fatalf("read_output(offset=%d limit=%d): result=%+v err=%v", offset, limit, res, err)
		}
		calls++
		body := res.Text
		if i := strings.Index(body, "\n"); i >= 0 {
			body = body[i+1:]
		}
		m := nextRe.FindStringSubmatch(res.Text)
		if m == nil {
			if !strings.Contains(res.Text, "[end of stored output]") {
				t.Fatalf("chunk has neither next offset nor end marker:\n%s", res.Text)
			}
			return calls, got + strings.TrimSuffix(body, "\n[end of stored output]")
		}
		got += strings.TrimSuffix(body, "\n"+m[0])
		offset, _ = strconv.Atoi(m[1])
	}
	t.Fatal("paging did not terminate")
	return 0, ""
}

// The regression this guards: the hint used to hardcode limit 4096 while the
// read_output schema allows 8192, so a 24 KB result cost twice the model round
// trips it needed. Each of those round trips is a full generation.
func TestOutputStore_HintPagesLargeResultInFewCalls(t *testing.T) {
	s := NewOutputStore()
	original := strings.Repeat("abcdefghij", 2432) + "123456789"
	if len(original) != 24329 {
		t.Fatalf("fixture is %d bytes, want 24329", len(original))
	}
	preview := s.Compact("read_many", original)
	offset, limit := parseHint(t, preview)

	calls, got := walkOutput(t, s, "out_000001", offset, limit)
	if calls > 3 {
		t.Errorf("paging a %d byte result took %d read_output calls, want <= 3", len(original), calls)
	}
	if want := original[offset:]; got != want {
		t.Errorf("paged content mismatch: got %d bytes, want %d", len(got), len(want))
	}

	oldCalls, _ := walkOutput(t, s, "out_000001", 4096, 4096)
	if oldCalls <= calls {
		t.Errorf("old hint took %d calls, new hint %d — no improvement", oldCalls, calls)
	}
	t.Logf("read_output calls: old hint (offset 4096, limit 4096) = %d, new hint (offset %d, limit %d) = %d",
		oldCalls, offset, limit, calls)
}

// A hint above the schema's maximum would be rejected as invalid args — the
// exact failure class this change exists to avoid.
func TestOutputStore_HintNeverExceedsSchemaMaximum(t *testing.T) {
	for _, total := range []int{outputInlineBytes + 1, 10000, 12287, 12288, 24329, 512 * 1024} {
		s := NewOutputStore()
		preview := s.Compact("read_many", strings.Repeat("x", total))
		offset, limit := parseHint(t, preview)
		if limit > outputReadMax {
			t.Errorf("total=%d: hint limit %d exceeds outputReadMax %d", total, limit, outputReadMax)
		}
		if limit <= 0 {
			t.Errorf("total=%d: hint limit %d is not a readable range", total, limit)
		}
		if offset+limit > total {
			t.Errorf("total=%d: hint reads %d:%d, past the end", total, offset, offset+limit)
		}
	}
}

// When the rest of the output fits in one allowed read, the hint must ask for
// exactly that much: no short chunk, no second call.
func TestOutputStore_HintFinishesShortRemainderInOneCall(t *testing.T) {
	s := NewOutputStore()
	original := strings.Repeat("y", 10000)
	preview := s.Compact("read_many", original)
	offset, limit := parseHint(t, preview)
	if offset+limit != len(original) {
		t.Errorf("hint reads %d:%d, want it to end exactly at %d", offset, offset+limit, len(original))
	}
	calls, got := walkOutput(t, s, "out_000001", offset, limit)
	if calls != 1 {
		t.Errorf("remainder took %d calls, want 1", calls)
	}
	if got != original[offset:] {
		t.Errorf("paged content mismatch: got %d bytes, want %d", len(got), len(original)-offset)
	}
}

// Results that fit inline keep the old behaviour exactly: no handle, no hint,
// byte-identical text.
func TestOutputStore_SmallResultHasNoPagingHint(t *testing.T) {
	s := NewOutputStore()
	for _, total := range []int{0, 1, 4096, outputInlineBytes} {
		original := strings.Repeat("z", total)
		got := s.Compact("read_many", original)
		if got != original {
			t.Errorf("total=%d: result changed (%d bytes out)", total, len(got))
		}
		if hintRe.MatchString(got) {
			t.Errorf("total=%d: small result carries a paging hint", total)
		}
	}
}

func TestOutputStore_EvictsLeastRecentlyUsed(t *testing.T) {
	s := NewOutputStore()
	for i := 0; i < outputStoreItems+1; i++ {
		if _, ok := s.put(strings.Repeat("x", 16)); !ok {
			t.Fatal("small output was not stored")
		}
	}
	if _, _, _, err := s.read("out_000001", 0, 1); err == nil {
		t.Fatal("oldest output survived the entry cap")
	}
	if got, _, _, err := s.read("out_000033", 0, 1); err != nil || got != "x" {
		t.Fatalf("newest output missing: got=%q err=%v", got, err)
	}
}
