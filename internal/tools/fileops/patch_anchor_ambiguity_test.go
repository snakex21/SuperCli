package fileops

import (
	"os"
	"strings"
	"testing"
)

func TestRelaxedPatchCountsAnchorsBeforeReindentingReplacement(t *testing.T) {
	for _, tc := range []struct {
		name, content, old, new string
	}{
		{"replacement cannot lose indentation", "value := 1\n    value := 1\n", "  value := 1   \n", "value := 2\n"},
		{"same ambiguity in reverse order", "    value := 1\nvalue := 1\n", "  value := 1   \n", "value := 2\n"},
		{"replacement incompatible with one indentation style", "    value := 1\n\t\tvalue := 1\n", "\tvalue := 1   \n", "\tvalue := 2\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "ambiguous.go", tc.content)
			_, err := PatchFile(path, []PatchChange{{Old: tc.old, New: tc.new, ExpectedCount: 1}}, "")
			if err == nil {
				t.Error("ambiguous anchor selected based on replacement indentation")
			}
			if err != nil && !strings.Contains(err.Error(), "nothing written") {
				t.Fatalf("missing failure status: %v", err)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil || string(data) != tc.content {
				t.Fatalf("ambiguous edit changed bytes: %q err=%v", data, readErr)
			}
		})
	}
}
