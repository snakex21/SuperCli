package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReadOutputOversizedLimitThroughRegistry(t *testing.T) {
	for _, tc := range []struct{ limit, offset int }{{12000, 0}, {14000, 3072}} {
		for _, query := range []string{"", "diagnostic"} {
			t.Run(fmt.Sprintf("limit=%d/offset=%d/query=%s", tc.limit, tc.offset, query), func(t *testing.T) {
				reg := NewRegistry()
				reg.EnsureReadOutput()
				original := strings.Repeat("routine log line\n", 600) + "diagnostic: failure in source.go\n" + strings.Repeat("routine log line\n", 600)
				reg.CompactModelOutput("ctx_execute", original)
				read := func(limit int) Result {
					t.Helper()
					raw, err := json.Marshal(readOutputArgs{Handle: onlyOutputHandle(t, reg.outputs), Offset: tc.offset, Limit: limit, Query: query})
					if err != nil {
						t.Fatal(err)
					}
					result, err := reg.Execute(context.Background(), "read_output", raw)
					if err != nil || result.Err != nil {
						t.Fatalf("limit=%d: %+v, %v", limit, result, err)
					}
					return result
				}
				got, capped := read(tc.limit), read(outputReadMax)
				if got.Text != capped.Text {
					t.Fatal("oversized request changed data or continuation instead of using output cap")
				}
				if query == "" {
					want := fmt.Sprintf("bytes %d:%d", tc.offset, tc.offset+outputReadMax)
					if !strings.Contains(got.Text, want) || !strings.Contains(got.Text, original[tc.offset:tc.offset+outputReadMax]) {
						t.Fatalf("wrong capped range: %s", got.Text)
					}
				} else if !strings.Contains(got.Text, "failure in source.go") {
					t.Fatal("lost diagnostic")
				}
				reg.ModelResultContent("read_output", got)
				if len(reg.outputs.entries) != 1 {
					t.Fatal("read_output recursively retained its own result")
				}
			})
		}
	}
}

func TestReadOutputOversizedLimitPagesUTF8Losslessly(t *testing.T) {
	s := NewOutputStore()
	original := strings.Repeat("ż中😀\n", 3000)
	s.Compact("read_many", original)
	calls, got := walkOutput(t, s, onlyOutputHandle(t, s), 0, 14000)
	if got != original {
		t.Fatal("oversized paging lost or duplicated UTF-8 content")
	}
	if calls != 4 {
		t.Fatalf("pages=%d, want 4 bounded pages", calls)
	}
}

func TestReadOutputLimitTolerancePreservesValidation(t *testing.T) {
	reg := NewRegistry()
	reg.EnsureReadOutput()
	reg.outputs.put("some data")
	for _, raw := range []string{
		`{"handle":"out_000001","limit":"many"}`,
		`{"handle":"out_000001","limit":1.5}`,
		`{"limit":12000}`,
		`{"handle":"missing","limit":12000}`,
		`{"handle":"out_000001","offset":-1,"limit":12000}`,
		`{"handle":"out_000001","offset":100,"limit":12000}`,
		fmt.Sprintf(`{"handle":"out_000001","query":%q,"limit":12000}`, strings.Repeat("q", 513)),
	} {
		result, err := reg.Execute(context.Background(), "read_output", json.RawMessage(strings.ReplaceAll(raw, "out_000001", onlyOutputHandle(t, reg.outputs))))
		if err == nil && result.Err == nil {
			t.Fatalf("accepted invalid args: %s", raw)
		}
	}
}
