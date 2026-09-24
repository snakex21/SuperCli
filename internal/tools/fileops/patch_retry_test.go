package fileops

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestFailedBatchRetryKeepsEveryChange(t *testing.T) {
	const original = "first=1\nsecond=2\nthird=3\n"
	for failed := 0; failed < 3; failed++ {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			path := writeTemp(t, "settings.txt", original)
			before, err := FileSHA256(path)
			if err != nil {
				t.Fatal(err)
			}
			changes := []PatchChange{{Old: "first=1", New: "first=10"}, {Old: "second=2", New: "second=20"}, {Old: "third=3", New: "third=30"}}
			correctOld := changes[failed].Old
			changes[failed].Old = "absent-setting=999"
			_, err = PatchFile(path, changes, before)
			if err == nil {
				t.Fatal("expected rejection")
			}
			if !strings.Contains(err.Error(), "nothing written") || !strings.Contains(err.Error(), fmt.Sprintf("fix change %d and resend all 3 changes", failed)) || strings.Contains(err.Error(), "alone") {
				t.Fatalf("wrong recovery guidance: %v", err)
			}
			unchanged, err := os.ReadFile(path)
			if err != nil || string(unchanged) != original || patchWriteCount(path) != 0 {
				t.Fatalf("rejected batch changed the file: %q %v", unchanged, err)
			}
			changes[failed].Old = correctOld
			result, err := PatchFile(path, changes, before)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "first=10\nsecond=20\nthird=30\n" {
				t.Fatalf("retry lost a change: %q %v", got, err)
			}
			if !result.Changed || result.Replacements != 3 || result.BeforeHash != before || patchWriteCount(path) != 1 {
				t.Fatalf("bad successful retry: %+v", result)
			}
		})
	}
}

func TestFailedBatchRetryPreservesDependentAndRelaxedChanges(t *testing.T) {
	original := "func f() {\r\n\tvalue := 1\r\n}\r\n"
	path := writeTemp(t, "dependent.go", original)
	changes := []PatchChange{
		{Old: "func f() {\n\tvalue := 1\n}", New: "func f() {\n\tstage := 2\n}"},
		{Old: "stage := 999", New: "final := 3"},
	}
	_, err := PatchFile(path, changes, "")
	if err == nil || !strings.Contains(err.Error(), "resend all 2 changes") {
		t.Fatalf("missing batch recovery: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Fatal("relaxed change escaped failed transaction")
	}
	changes[1].Old = "stage := 2"
	if _, err := PatchFile(path, changes, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "func f() {\r\n\tfinal := 3\r\n}\r\n" {
		t.Fatalf("dependent retry incorrect: %q", got)
	}
}
