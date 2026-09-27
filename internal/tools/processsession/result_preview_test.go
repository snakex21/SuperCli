package processsession

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"supercli/internal/tools/core"
)

func TestProcessPreviewKeepsStateAndBoundedStreams(t *testing.T) {
	samples := []string{"", strings.Repeat("normal log\n", 1000), strings.Repeat("zażółć 😀 漢字\n", 1000), strings.Repeat("\\\"\t\r\n\x00<>&\u2028\u2029", 1000)}
	for _, state := range []string{"running", "done", "stopped"} {
		for i, stdout := range samples {
			for j, stderr := range samples {
				t.Run(fmt.Sprintf("%s/%d/%d", state, i, j), func(t *testing.T) {
					code := 0
					if state == "stopped" {
						code = 1
					}
					snap := snapshot{ID: "proc-18446744073709551615", Status: state, DurationMS: math.MaxInt64, PTY: true, Stdout: stdout, Stderr: stderr, Command: []string{strings.Repeat("command", 2000)}, Workdir: "project", OmittedErr: 3}
					if state != "running" {
						snap.ExitCode = &code
					}
					before, _ := json.Marshal(snap)
					body := snap.modelPreview()
					if len(body) > core.ModelOutputPreviewBytes || !json.Valid([]byte(body)) || !utf8.ValidString(body) {
						t.Fatalf("bad preview: %d bytes", len(body))
					}
					var got struct {
						snapshot
						Preview         bool `json:"preview"`
						TruncatedStdout bool `json:"truncated_stdout"`
						TruncatedStderr bool `json:"truncated_stderr"`
					}
					if err := json.Unmarshal([]byte(body), &got); err != nil {
						t.Fatal(err)
					}
					if got.ID != snap.ID || got.Status != state || got.DurationMS != snap.DurationMS || !got.PTY || !got.Preview || got.OmittedErr != 3 {
						t.Fatalf("metadata lost: %s", body)
					}
					if (got.ExitCode == nil) != (snap.ExitCode == nil) || got.ExitCode != nil && *got.ExitCode != code {
						t.Fatal("exit status changed")
					}
					if len(got.Command) != 0 || got.Workdir != "" {
						t.Fatal("duplicated argv/workdir consumed preview budget")
					}
					for _, stream := range []struct {
						want, got string
						shortened bool
					}{{stdout, got.Stdout, got.TruncatedStdout}, {stderr, got.Stderr, got.TruncatedStderr}} {
						if !stream.shortened && stream.want != stream.got {
							t.Fatal("silent truncation")
						}
						if stream.want != stream.got && !strings.Contains(stream.got, "[... output omitted from preview ...]") {
							t.Fatal("omission marker lost")
						}
					}
					after, _ := json.Marshal(snap)
					if string(before) != string(after) {
						t.Fatal("original snapshot modified")
					}
				})
			}
		}
	}
	if got := (snapshot{ID: strings.Repeat("x", 5000)}).modelPreview(); got != "" {
		t.Fatal("oversized metadata did not fall back")
	}
}

func TestProcessPreviewOnlyChangesLargeNonFailureModelView(t *testing.T) {
	for _, state := range []string{"running", "done", "failed", "stopped"} {
		for _, large := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/large=%t", state, large), func(t *testing.T) {
				tool := New(t.TempDir())
				// Completed in-memory snapshot: no process or polling loop exists.
				size := 1
				if large {
					size = 2000
				}
				out := strings.Repeat("full evidence\n", size)
				item := &process{id: "proc-1", status: state, exitCode: 0, started: time.Now(), stdout: newStreamBuffer(maxBufferBytes), stderr: newStreamBuffer(maxBufferBytes)}
				if state != "running" {
					item.ended = time.Now()
				}
				if state == "failed" || state == "stopped" {
					item.exitCode = 7
				}
				_, _ = item.stdout.Write([]byte(out))
				tool.Manager.items[item.id] = item
				result, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"poll","id":"proc-1"}`))
				if err != nil {
					t.Fatal(err)
				}
				wantPreview := large && state != "failed"
				if (result.ModelPreview != "") != wantPreview {
					t.Fatalf("preview=%t want=%t", result.ModelPreview != "", wantPreview)
				}
				var original snapshot
				if err := json.Unmarshal([]byte(result.Text), &original); err != nil || original.Stdout != out[:min(len(out), maxPollBytes)] || original.Status != state {
					t.Fatalf("original UI result changed: %v", err)
				}
				if (result.Err != nil) != (state == "failed") {
					t.Fatal("tool outcome changed")
				}
				store := core.NewOutputStore()
				model := store.ModelContent("process_session", result)
				if wantPreview && (core.StoredOutputHandle(model) == "" || !strings.HasPrefix(model, "{")) {
					t.Fatal("large result not retained behind structured preview")
				}
				if !large && state != "failed" && model != result.Text {
					t.Fatal("small result changed")
				}
			})
		}
	}
}
