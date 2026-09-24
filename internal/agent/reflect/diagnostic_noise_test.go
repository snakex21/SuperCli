package reflect

import (
	"context"
	"testing"
	"time"
)

func TestExtractorSkipsClassifierFallback(t *testing.T) {
	path := DefaultErrorsPath(t.TempDir())
	writeErrors(t, path, []ErrorRecord{
		{Timestamp: time.Now(), Tool: "read_lines", Category: "model", Reason: "no heuristic matched; defaulting to model"},
		{Timestamp: time.Now(), Tool: "read_lines", Category: "model", Reason: "file not found"},
	})
	got, err := (&Extractor{ErrorsPath: path}).Extract(context.Background())
	if err != nil || len(got) != 1 || got[0].Reason != "file not found" {
		t.Fatalf("%+v %v", got, err)
	}
}
