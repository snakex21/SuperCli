package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"supercli/internal/tools/core"
	"supercli/internal/tools/fileops"
	"supercli/internal/tools/sandbox"
)

func downloadReplayFixture(t *testing.T, ctx context.Context, path string, body []byte) (*WebDownload, *int, json.RawMessage, Result) {
	t.Helper()
	tool, calls := downloadFixture(t, io.NopCloser(bytes.NewReader(body)), "application/octet-stream", int64(len(body)), http.StatusOK)
	args, _ := json.Marshal(webDownloadArgs{URL: "https://arbitrary.example.test/file?token=never-echo-secret", Path: path})
	prior, err := tool.Spec().Fn(ctx, args)
	if err != nil || prior.Err != nil || prior.Operation == nil {
		t.Fatalf("initial download has no verified operation: %+v %v", prior, err)
	}
	return tool, calls, args, prior
}

func TestWebDownloadReplayVerifiesSameEffectWithoutHTTP(t *testing.T) {
	body := bytes.Repeat([]byte("saved asset\x00"), 5000)
	tool, calls, args, prior := downloadReplayFixture(t, context.Background(), "assets/portable.bin", body)
	if tool.Spec().ReadOnly || tool.Spec().ReplaySuccess == nil || prior.Inert {
		t.Fatal("download must remain a mutation with explicit checked replay")
	}
	for range 2 {
		result, handled := tool.Spec().ReplaySuccess(context.Background(), args, prior)
		if !handled || result.Err != nil || !result.Inert || result.Operation != prior.Operation || *calls != 1 {
			t.Fatalf("receipt replay repeated effect or lost evidence: %+v handled=%v calls=%d", result, handled, *calls)
		}
		if !strings.Contains(result.Text, "No HTTP request made") || strings.Contains(result.Text, "never-echo-secret") {
			t.Fatalf("unsafe replay result: %s", result.Text)
		}
	}
	full := filepath.Join(tool.BaseDir, "assets", "portable.bin")
	quotedPath, _ := json.Marshal(full)
	if !strings.Contains(prior.Operation.Summary, string(quotedPath)) || !strings.Contains(prior.Operation.Summary, fmt.Sprintf("%d bytes", len(body))) {
		t.Fatalf("operation summary lost exact destination or measured size: %s", prior.Operation.Summary)
	}
	serialized, err := json.Marshal(prior)
	if err != nil || strings.Contains(string(serialized), "Operation") || strings.Contains(string(serialized), "Evidence") || strings.Contains(string(serialized), "never-echo-secret") {
		t.Fatalf("runtime operation leaked into persistence: %s %v", serialized, err)
	}
	serialized, err = json.Marshal(prior.Operation)
	if err != nil || strings.Contains(string(serialized), "Evidence") || strings.Contains(string(serialized), "never-echo-secret") {
		t.Fatalf("private receipt leaked signed source: %s %v", serialized, err)
	}
}

func TestWebDownloadReplayRejectsChangedFileEvenWithPreservedSizeAndTime(t *testing.T) {
	body := []byte("original bytes")
	tool, calls, args, prior := downloadReplayFixture(t, context.Background(), "asset.bin", body)
	path := filepath.Join(tool.BaseDir, "asset.bin")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte("modified bytes")
	if err := os.WriteFile(path, changed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	result, handled := tool.Spec().ReplaySuccess(context.Background(), args, prior)
	if !handled || result.Err == nil || result.Operation != nil || result.Inert || *calls != 1 {
		t.Fatalf("changed content was accepted as completed: %+v handled=%v calls=%d", result, handled, *calls)
	}
	saved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(saved, changed) {
		t.Fatalf("replay overwrote changed file: %q %v", saved, err)
	}
}

func TestWebDownloadReplayRejectsReplacementAndNonRegularFiles(t *testing.T) {
	for _, kind := range []string{"replacement", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			body := []byte("original bytes")
			tool, calls, args, prior := downloadReplayFixture(t, context.Background(), "asset.bin", body)
			path := filepath.Join(tool.BaseDir, "asset.bin")
			moved := filepath.Join(tool.BaseDir, "original.bin")
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "replacement":
				if err := os.WriteFile(path, body, 0o644); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(moved, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			result, handled := tool.Spec().ReplaySuccess(context.Background(), args, prior)
			if !handled || result.Err == nil || result.Operation != nil || result.Inert || *calls != 1 {
				t.Fatalf("replacement/reparse accepted as completed: %+v handled=%v calls=%d", result, handled, *calls)
			}
		})
	}
}

func TestWebDownloadReplayMissingFileDeclinesWithoutHTTP(t *testing.T) {
	tool, calls, args, prior := downloadReplayFixture(t, context.Background(), "asset.bin", []byte("asset"))
	if err := os.Remove(filepath.Join(tool.BaseDir, "asset.bin")); err != nil {
		t.Fatal(err)
	}
	result, handled := tool.Spec().ReplaySuccess(context.Background(), args, prior)
	if handled || result.Err != nil || result.Operation != nil || result.Inert || *calls != 1 {
		t.Fatalf("missing file was accepted or redownloaded in replay: %+v handled=%v calls=%d", result, handled, *calls)
	}
}

func TestWebDownloadReplayRequiresOwnReceiptAndSameArguments(t *testing.T) {
	tool, calls, args, prior := downloadReplayFixture(t, context.Background(), "asset.bin", []byte("asset"))
	for _, fake := range []Result{
		{},
		{Text: prior.Text},
		{Text: prior.Text, Operation: &core.VerifiedOperation{Summary: prior.Operation.Summary, Evidence: "untrusted receipt text"}},
		{Text: prior.Text, Err: errors.New("failed"), Operation: prior.Operation},
	} {
		result, handled := tool.Spec().ReplaySuccess(context.Background(), args, fake)
		if handled || result.Operation != nil || *calls != 1 {
			t.Fatalf("unverified/foreign receipt accepted: %+v handled=%v calls=%d", result, handled, *calls)
		}
	}
	for _, changed := range []webDownloadArgs{
		{URL: "https://other.example.test/different?token=secret", Path: "asset.bin"},
		{URL: "https://arbitrary.example.test/file?token=never-echo-secret", Path: "asset.bin", MaxBytes: webDownloadDefaultBytes + 1},
	} {
		raw, _ := json.Marshal(changed)
		result, handled := tool.Spec().ReplaySuccess(context.Background(), raw, prior)
		if handled || result.Operation != nil || *calls != 1 {
			t.Fatalf("different invocation replayed: %+v handled=%v calls=%d", result, handled, *calls)
		}
	}
}

func TestWebDownloadReplayRechecksCurrentExportPermission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exports", "asset.bin")
	ctx := sandbox.WithDownloadExportTargets(context.Background(), nil, []string{path})
	tool, calls, args, prior := downloadReplayFixture(t, ctx, path, []byte("asset"))
	allowed, handled := tool.Spec().ReplaySuccess(ctx, args, prior)
	if !handled || allowed.Err != nil || !allowed.Inert {
		t.Fatalf("same exact-file authorization should replay: %+v handled=%v", allowed, handled)
	}
	result, handled := tool.Spec().ReplaySuccess(context.Background(), args, prior)
	if !handled || result.Err == nil || result.Operation != nil || result.Inert || *calls != 1 {
		t.Fatalf("receipt became an export permission: %+v handled=%v calls=%d", result, handled, *calls)
	}
}

func TestWebDownloadReplayCancellationDoesNotClaimSuccess(t *testing.T) {
	body := bytes.Repeat([]byte("asset"), 100_000)
	tool, calls, args, prior := downloadReplayFixture(t, context.Background(), "asset.bin", body)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{canceled, &countedReplayCancelContext{Context: context.Background(), cancelAt: 12}} {
		result, handled := tool.Spec().ReplaySuccess(ctx, args, prior)
		if !handled || !errors.Is(result.Err, context.Canceled) || result.Operation != nil || result.Inert || *calls != 1 {
			t.Fatalf("canceled verification accepted: %+v handled=%v calls=%d", result, handled, *calls)
		}
	}
	saved, err := os.ReadFile(filepath.Join(tool.BaseDir, "asset.bin"))
	if err != nil || sha256.Sum256(saved) != sha256.Sum256(body) {
		t.Fatalf("canceled replay altered file: %v", err)
	}
}

// Cancellation after several context checks exercises the streaming path
// without a timer, progress polling or an artificial slow file reader.
type countedReplayCancelContext struct {
	context.Context
	calls    atomic.Int32
	cancelAt int32
}

func (c *countedReplayCancelContext) Err() error {
	if c.calls.Add(1) >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestWebDownloadReplayWaitForMutationLockIsCancelable(t *testing.T) {
	tool, calls, args, prior := downloadReplayFixture(t, context.Background(), "asset.bin", []byte("asset"))
	release := fileops.LockMutationPaths(filepath.Join(tool.BaseDir, "asset.bin"))
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	checked := make(chan struct{})
	notifying := &notifyingReplayContext{Context: ctx, checked: checked}
	done := make(chan Result, 1)
	go func() {
		result, handled := tool.Spec().ReplaySuccess(notifying, args, prior)
		if !handled {
			result.Err = errors.New("canceled replay was not handled")
		}
		done <- result
	}()
	<-checked
	cancel()
	result := <-done
	if !errors.Is(result.Err, context.Canceled) || result.Operation != nil || *calls != 1 {
		t.Fatalf("Stop cannot cancel queued receipt verification: %+v calls=%d", result, *calls)
	}
}

type notifyingReplayContext struct {
	context.Context
	calls   atomic.Int32
	checked chan struct{}
}

func (c *notifyingReplayContext) Err() error {
	if c.calls.Add(1) == 3 {
		close(c.checked)
	}
	return c.Context.Err()
}
