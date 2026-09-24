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

func searchOutput(t *testing.T, s *OutputStore, query string, offset, limit int) string {
	t.Helper()
	raw, err := json.Marshal(readOutputArgs{Handle: onlyOutputHandle(t, s), Query: query, Offset: offset, Limit: limit})
	if err != nil {
		t.Fatal(err)
	}
	// Use Registry.Execute to cover the exposed schema, not just the helper.
	reg := NewRegistry()
	reg.MustRegister(s.ReadOutputTool())
	result, err := reg.Execute(context.Background(), "read_output", raw)
	if err != nil || result.Err != nil {
		t.Fatalf("search: %v %+v", err, result)
	}
	return result.Text
}

func TestReadOutputSearchFindsHiddenDiagnosticInOneCall(t *testing.T) {
	s := NewOutputStore()
	text := strings.Repeat("routine log line\n", 5000) + "compiler: source.go:731 undefined: missingSymbol\n" + strings.Repeat("routine log line\n", 5000)
	preview := s.Compact("ctx_execute", text)
	if strings.Contains(preview, "missingSymbol") {
		t.Fatal("fixture must hide the diagnostic from the preview")
	}
	found := searchOutput(t, s, "missingSymbol", 0, 0)
	if !strings.Contains(found, "source.go:731") || !strings.Contains(found, "missingSymbol") {
		t.Fatalf("diagnostic missing: %s", found)
	}
	if len(found) > 1024 {
		t.Fatalf("search injected too much context: %d bytes", len(found))
	}
	if strings.Contains(found, "next search offset") {
		t.Fatal("single match should finish without more calls")
	}
}

func TestReadOutputSearchLiteralAndUTF8(t *testing.T) {
	s := NewOutputStore()
	query := "Żółć.[a-z]*😀"
	text := strings.Repeat("ą", 8000) + query + strings.Repeat("ę", 8000)
	s.Compact("read_many", text)
	found := searchOutput(t, s, query, 0, 600)
	if !strings.Contains(found, query) || !utf8.ValidString(found) {
		t.Fatalf("lost literal/UTF-8: %q", found)
	}
	missing := searchOutput(t, s, "żółć.[a-z]*😀", 0, 600)
	if !strings.Contains(missing, "[no matches]") {
		t.Fatal("search must be case-sensitive")
	}
}

func TestReadOutputSearchPagesBoundedExcerptsWithoutLostMatches(t *testing.T) {
	s := NewOutputStore()
	var source strings.Builder
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&source, "%s error[%02d]\n", strings.Repeat("x", 1000), i)
	}
	s.Compact("ctx_execute", source.String())
	nextPattern := regexp.MustCompile(`next search offset: ([0-9]+)`)
	offset := 0
	var all strings.Builder
	for page := 0; page < 30; page++ {
		text := searchOutput(t, s, "error[", offset, 400)
		if len(text) > 1200 {
			t.Fatalf("unbounded search response: %d", len(text))
		}
		all.WriteString(text)
		match := nextPattern.FindStringSubmatch(text)
		if match == nil {
			for i := 0; i < 25; i++ {
				if !strings.Contains(all.String(), fmt.Sprintf("error[%02d]", i)) {
					t.Fatalf("lost match %d", i)
				}
			}
			return
		}
		next, _ := strconv.Atoi(match[1])
		if next <= offset {
			t.Fatalf("pagination did not advance: %d -> %d", offset, next)
		}
		offset = next
	}
	t.Fatal("search did not terminate")
}

func TestReadOutputSearchFindsMatchCrossingExcerptBoundary(t *testing.T) {
	s := NewOutputStore()
	// With limit=4, the first excerpt is 'AB--'; the next match starts before
	// that excerpt ends and must still be returned in the next page.
	s.put("AB--AB--AB")
	first := searchOutput(t, s, "AB", 0, 4)
	if !strings.Contains(first, "[next search offset: 4]") {
		t.Fatalf("continuation: %s", first)
	}
	next := searchOutput(t, s, "AB", 4, 4)
	if !strings.Contains(next, "AB") || !strings.Contains(next, "[next search offset: 8]") {
		t.Fatalf("next match missing: %s", next)
	}
	// A genuine overlapping boundary: first excerpt 'ABCAB', next match ABC
	// begins at 3 and ends at 6, after the first excerpt's end.
	s = NewOutputStore()
	s.put("ABCABCXYZ")
	first = searchOutput(t, s, "ABC", 0, 5)
	if !strings.Contains(first, "[next search offset: 3]") {
		t.Fatalf("crossing match lost: %s", first)
	}
}

func TestReadOutputSearchErrorsAndCancellation(t *testing.T) {
	s := NewOutputStore()
	oldHandle, _ := s.put("diagnostic")
	for _, args := range []readOutputArgs{
		{Handle: "missing", Query: "diagnostic"},
		{Handle: onlyOutputHandle(t, s), Query: "a", Offset: -1},
		{Handle: onlyOutputHandle(t, s), Query: "a", Offset: 11},
		{Handle: onlyOutputHandle(t, s), Query: strings.Repeat("a", 513)},
		{Handle: onlyOutputHandle(t, s), Query: "diagnostic", Limit: 2},
	} {
		if _, err := s.search(context.Background(), args); err == nil {
			t.Fatalf("accepted invalid args: %+v", args)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.search(ctx, readOutputArgs{Handle: onlyOutputHandle(t, s), Query: "a"}); err != context.Canceled {
		t.Fatalf("cancellation=%v", err)
	}
	// Search must use the same expiry policy as paging.
	for i := 0; i < outputStoreItems; i++ {
		s.put("new result")
	}
	if _, err := s.search(context.Background(), readOutputArgs{Handle: oldHandle, Query: "a"}); err == nil {
		t.Fatal("expired output was searchable")
	}
}
