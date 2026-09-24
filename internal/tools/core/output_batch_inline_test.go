package core

import (
	"strings"
	"testing"
)

func TestBatchReadInlineBoundaryDoesNotChangeOtherTools(t *testing.T) {
	for _, name := range []string{"read_many", "read_lines", "search_code", "ctx_execute"} {
		for _, size := range []int{8192, 8193, 12288, 12289} {
			text := strings.Repeat("x", size)
			store := NewOutputStore()
			got := store.ModelContent(name, Result{Text: text})
			limit := 8192
			if name == "read_many" {
				limit = 12288
			}
			if (got == text) != (size <= limit) {
				t.Fatalf("%s %d bytes: inline=%v", name, size, got == text)
			}
			if size > limit && (!strings.Contains(got, "handle=") || len(got) > 4600) {
				t.Fatalf("%s %d: missing bounded retained output", name, size)
			}
		}
	}
}
