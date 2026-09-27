package ctxexec

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"supercli/internal/tools/core"
)

func TestSuccessPreviewBudgetAndEncoding(t *testing.T) {
	samples := []string{"", strings.Repeat("plain log line\n", 600), strings.Repeat("zażółć 😀 漢字\n", 800), strings.Repeat("\\\"\t\r\n\x00<>&\u2028\u2029", 900), strings.Repeat(string([]byte{0xff, 0xc0, 0x80}), 1000)}
	for i, stdout := range samples {
		for j, stderr := range samples {
			t.Run(fmt.Sprintf("%d/%d", i, j), func(t *testing.T) {
				r := Result{Stdout: stdout, Stderr: stderr, DurationMS: math.MaxInt64, Command: strings.Repeat("command ", 1000), Workdir: "original workdir"}
				original := r
				body := r.SuccessPreview()
				if len(body) > core.ModelOutputPreviewBytes || !utf8.ValidString(body) || !json.Valid([]byte(body)) {
					t.Fatalf("invalid preview: bytes=%d", len(body))
				}
				var got Result
				if err := json.Unmarshal([]byte(body), &got); err != nil {
					t.Fatal(err)
				}
				if got.ExitCode != 0 || got.DurationMS != r.DurationMS || got.Command != "" || got.Workdir != "" {
					t.Fatal("metadata changed")
				}
				normalized := func(s string) string {
					raw, _ := json.Marshal(s)
					var out string
					_ = json.Unmarshal(raw, &out)
					return out
				}
				for _, stream := range []struct {
					want, got string
					truncated bool
				}{{normalized(stdout), got.Stdout, got.TruncatedStdout}, {normalized(stderr), got.Stderr, got.TruncatedStderr}} {
					if !stream.truncated && stream.want != stream.got {
						t.Fatal("silent stream truncation")
					}
					if stream.truncated && !strings.Contains(stream.got, "[... output omitted from preview ...]") {
						t.Fatal("missing omission marker")
					}
				}
				if r != original {
					t.Fatal("original result modified")
				}
			})
		}
	}
}

func TestSuccessPreviewPreservesCaptureFlagsAndRejectsErrors(t *testing.T) {
	r := Result{Stdout: "small", TruncatedStdout: true, TruncatedStderr: true}
	var got Result
	if err := json.Unmarshal([]byte(r.SuccessPreview()), &got); err != nil || !got.TruncatedStdout || !got.TruncatedStderr {
		t.Fatal("capture flags lost")
	}
	r.ExitCode = 1
	if r.SuccessPreview() != "" {
		t.Fatal("failure rendered as success")
	}
	r.ExitCode = 0
	r.Error = "capture failure"
	if r.SuccessPreview() != "" {
		t.Fatal("capture error rendered as success")
	}
	var missing *Result
	if missing.SuccessPreview() != "" {
		t.Fatal("nil result rendered as success")
	}
}

func TestBoundedJSONStreamEveryCut(t *testing.T) {
	raw, _ := json.Marshal(strings.Repeat("ab\\\"\n<>&\x01é漢😀\u2028", 25))
	for budget := 2; budget <= len(raw)+1; budget++ {
		got, truncated := core.PreviewJSONString(raw, budget)
		if len(got) > budget || !json.Valid(got) || truncated != (len(raw) > budget) {
			t.Fatalf("bad cut at %d: len=%d, valid=%t", budget, len(got), json.Valid(got))
		}
	}
}

func BenchmarkCommandModelPreview(b *testing.B) {
	for _, size := range []int{64, 16 * 1024} {
		r := Result{Stdout: strings.Repeat("stdout\n", size/7), Stderr: strings.Repeat("warning\n", size/32), Command: strings.Repeat("arg ", 300), Workdir: "project"}
		for _, structured := range []bool{false, true} {
			b.Run(fmt.Sprintf("size=%d/structured=%t", size, structured), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					raw, _ := json.Marshal(r)
					body := string(raw)
					if len(body) > core.ModelOutputInlineBytes {
						if structured {
							body = r.SuccessPreview()
						} else {
							body = core.HeadTail(body, 3072, 1024)
						}
					}
					if len(body) == 0 {
						b.Fatal("empty preview")
					}
				}
			})
		}
	}
}
