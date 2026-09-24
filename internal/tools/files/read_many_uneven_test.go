package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/core"
)

func TestReadManyUnevenBatchUsesAvailableInlineBudget(t *testing.T) {
	root := t.TempDir()
	var requests []string
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("file%d.txt", i)
		text := "Flag=off\n"
		if i == 5 {
			var b strings.Builder
			for n := 1; n <= 100; n++ {
				if n == 55 {
					b.WriteString("BudgetLimit=6842\n")
				} else {
					fmt.Fprintf(&b, "line_%03d padding abcdefghijklmnopqrstuvwxyz0123456789\n", n)
				}
			}
			text = b.String()
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, name+":1-100")
	}
	args, _ := json.Marshal(map[string]any{"reads": strings.Join(requests, " | ")})
	result, err := NewReadMany(root).Spec().Fn(context.Background(), args)
	if err != nil || result.Err != nil {
		t.Fatalf("%+v %v", result, err)
	}
	if !strings.Contains(result.Text, "BudgetLimit=6842") || result.RetainedText != "" || result.ModelPreview != "" {
		t.Fatalf("discarded requested evidence: %+v", result)
	}
	if len(result.Text) > core.ModelReadBatchInlineBytes {
		t.Fatal("inline bound exceeded")
	}
	if got := core.NewOutputStore().ModelContent("read_many", result); got != result.Text {
		t.Fatal("complete batch was compacted")
	}
	for i := 0; i < 12; i++ {
		if !strings.Contains(result.Text, fmt.Sprintf("== [%d] file%d.txt:1-100 ==", i+1, i)) {
			t.Fatalf("missing file%d", i)
		}
	}
}

func unevenOutcomes() []readManyOutcome {
	out := make([]readManyOutcome, 12)
	for i := range out {
		out[i] = readManyOutcome{request: readManyRequest{File: fmt.Sprintf("file%d.txt", i), From: 1, To: 100}, text: "tiny\n"}
	}
	out[5].text = strings.Repeat("x", 6000) + "\n"
	return out
}

func completeBatchSize(out []readManyOutcome) int {
	total := len(fmt.Sprintf("[read_many: %d ok, 0 failed]", len(out)))
	for i, o := range out {
		total += len(fmt.Sprintf("== [%d] %s:%d-%d ==\n", i+1, o.request.File, o.request.From, o.request.To)) + len(o.text)
		if !strings.HasSuffix(o.text, "\n") {
			total++
		}
	}
	return total
}

func TestReadManyUnevenInlineBoundaries(t *testing.T) {
	for _, size := range []int{core.ModelReadBatchInlineBytes - 1, core.ModelReadBatchInlineBytes, core.ModelReadBatchInlineBytes + 1} {
		out := unevenOutcomes()
		remaining := size - completeBatchSize(out)
		out[6].text = strings.Repeat("y", remaining+len(out[6].text)-1) + "\n"
		if completeBatchSize(out) != size {
			t.Fatal("incorrect boundary fixture")
		}
		got := renderReadMany(out)
		complete := got.RetainedText == "" && got.ModelPreview == "" && len(got.Text) == size
		if complete != (size <= core.ModelReadBatchInlineBytes) {
			t.Fatalf("size=%d text=%d retained=%d preview=%d", size, len(got.Text), len(got.RetainedText), len(got.ModelPreview))
		}
	}
	for _, size := range []int{maxReadManyItem, maxReadManyItem + 1} {
		out := unevenOutcomes()
		out[5].text = strings.Repeat("x", size-1) + "\n"
		got := renderReadMany(out)
		complete := got.RetainedText == "" && got.ModelPreview == ""
		if complete != (size <= maxReadManyItem) {
			t.Fatalf("item size=%d: retained=%d", size, len(got.RetainedText))
		}
	}
}

func TestReadManyUnevenFailedAndSingleKeepRetention(t *testing.T) {
	out := unevenOutcomes()
	out[0].err = errors.New("missing file")
	got := renderReadMany(out)
	if !strings.Contains(got.Text, "error: missing file") || got.RetainedText == "" {
		t.Fatal("failed batch policy changed")
	}
	single := []readManyOutcome{{request: readManyRequest{File: "large.txt", From: 1, To: 100}, text: strings.Repeat("x", 9000)}}
	got = renderReadMany(single)
	if got.RetainedText == "" || got.ModelPreview == "" {
		t.Fatal("single-file bound changed")
	}
}

func BenchmarkReadManyUnevenRendering(b *testing.B) {
	for _, kind := range []string{"uneven", "balanced", "large", "failed"} {
		out := unevenOutcomes()
		if kind == "balanced" {
			for i := range out {
				out[i].text = strings.Repeat("z", 500) + "\n"
			}
		}
		if kind == "large" {
			for i := range out {
				out[i].text = strings.Repeat("z", 4000) + "\n"
			}
		}
		if kind == "failed" {
			out[0].err = errors.New("missing file")
		}
		b.Run(kind, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = renderReadMany(out)
			}
		})
	}
}
