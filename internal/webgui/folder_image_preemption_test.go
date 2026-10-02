package webgui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
)

type imageIndexProviderFunc struct {
	complete func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error)
}

func (p imageIndexProviderFunc) Name() string { return "echo-test" }
func (p imageIndexProviderFunc) Complete(ctx context.Context, messages []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
	return p.complete(ctx, messages, tools)
}

func writeImageIndexFixture(t *testing.T) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDescribeIndexedImageCanceledStreamDiscardsPartial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := imageIndexProviderFunc{complete: func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
		stream := make(chan llm.Delta, 1)
		stream <- llm.Delta{Content: "incomplete caption"}
		cancel()
		close(stream) // Some providers close silently on cancellation.
		return stream, nil
	}}
	caption, err := describeIndexedImage(ctx, provider, writeImageIndexFixture(t), "en")
	if !errors.Is(err, context.Canceled) || caption != "" {
		t.Fatalf("canceled stream returned (%q, %v), want no caption and cancellation", caption, err)
	}
}

// These tests use the actual Metered foreground/background coordinator, with
// controlled local channels instead of model or HTTP requests.
func TestIndexedImageForegroundPreemptionRetriesBeforeCaching(t *testing.T) {
	for _, mode := range []string{"silent_stream", "error_delta", "before_stream", "cancel_parent"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			runCtx, cancelRun := context.WithCancel(ctx)
			defer cancelRun()
			imagePath := writeImageIndexFixture(t)
			documents := filepath.Dir(imagePath)
			dataDir := t.TempDir()
			eng, err := NewEngine(echoConfig(), documents, dataDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = eng.Close() })
			firstStarted := make(chan struct{})
			firstCanceled := make(chan struct{}, 1)
			wrapperStats := make(chan llm.CallStat, 8)
			outerStats := make(chan llm.CallStat, 8)
			requests := make(chan string, 3)
			deadlines := make(chan time.Time, 3)
			var attempts atomic.Int32
			provider := imageIndexProviderFunc{complete: func(callCtx context.Context, messages []llm.Message, tools []llm.ToolDef) (<-chan llm.Delta, error) {
				if llm.PurposeFromContext(callCtx) != "vision-index" {
					stream := make(chan llm.Delta, 1)
					stream <- llm.Delta{Content: "complete folder note"}
					close(stream)
					return stream, nil
				}
				if !llm.IsBackground(callCtx) {
					return nil, fmt.Errorf("image analysis must be background work")
				}
				request, _ := json.Marshal(messages)
				requests <- string(request)
				deadline, ok := callCtx.Deadline()
				if !ok {
					return nil, fmt.Errorf("missing image-analysis deadline")
				}
				deadlines <- deadline
				if attempts.Add(1) == 1 {
					if mode == "before_stream" {
						close(firstStarted)
						<-callCtx.Done()
						return nil, callCtx.Err()
					}
					stream := make(chan llm.Delta, 2)
					stream <- llm.Delta{Content: "incomplete caption"}
					close(firstStarted)
					go func() {
						<-callCtx.Done()
						if mode == "error_delta" {
							stream <- llm.Delta{Err: callCtx.Err()}
						}
						close(stream)
					}()
					return stream, nil
				}
				stream := make(chan llm.Delta, 1)
				stream <- llm.Delta{Content: "<think>hidden</think> A complete image caption."}
				close(stream)
				return stream, nil
			}}
			eng.prov = llm.Metered(provider, "test", "main", func(stat llm.CallStat) {
				wrapperStats <- stat
				if stat.Purpose == "vision-index" && stat.Canceled {
					firstCanceled <- struct{}{}
				}
			})
			config := defaultFolderIndexConfig()
			config.VisionModel = "echo-test"
			config.VisualIndex = true
			server := NewServer(eng, false)
			runCtx = llm.WithCallSink(runCtx, func(stat llm.CallStat) { outerStats <- stat })
			type indexed struct {
				results []folderScanResult
				at      string
				err     error
			}
			done := make(chan indexed, 1)
			go func() {
				results, at, err := server.indexFolderPaths(runCtx, []string{documents}, config, nil)
				done <- indexed{results, at, err}
			}()
			select {
			case <-firstStarted:
			case <-ctx.Done():
				t.Fatal("image analysis did not start")
			}
			foregroundRelease := make(chan struct{})
			var releaseOnce sync.Once
			releaseForeground := func() { releaseOnce.Do(func() { close(foregroundRelease) }) }
			defer releaseForeground()
			foreground := llm.Metered(imageIndexProviderFunc{complete: func(callCtx context.Context, _ []llm.Message, _ []llm.ToolDef) (<-chan llm.Delta, error) {
				stream := make(chan llm.Delta)
				go func() {
					defer close(stream)
					select {
					case <-foregroundRelease:
					case <-callCtx.Done():
					}
				}()
				return stream, nil
			}}, "test", "main", func(llm.CallStat) {})
			stream, err := foreground.Complete(ctx, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			foregroundDone := make(chan struct{})
			go func() {
				for range stream {
				}
				close(foregroundDone)
			}()
			select {
			case <-firstCanceled:
			case <-ctx.Done():
				t.Fatal("foreground did not preempt image analysis")
			}
			if attempts.Load() != 1 {
				t.Fatal("image retry entered the provider while foreground was active")
			}
			if mode == "cancel_parent" {
				cancelRun()
			} else {
				releaseForeground()
			}
			var result indexed
			select {
			case result = <-done:
			case <-ctx.Done():
				t.Fatal("image retry/cancellation did not finish")
			}
			releaseForeground()
			select {
			case <-foregroundDone:
			case <-ctx.Done():
				t.Fatal("foreground stream did not close")
			}
			cache := loadFolderIndexCache(dataDir)
			cached, cachedImage := cache.Files[folderCacheKey(imagePath)]
			if mode == "cancel_parent" {
				if !errors.Is(result.err, context.Canceled) || result.at != "" || cachedImage || attempts.Load() != 1 {
					t.Fatalf("parent cancellation saved/finished partial index: err=%v at=%q cache=%+v calls=%d", result.err, result.at, cached, attempts.Load())
				}
			} else {
				if result.err != nil || result.at == "" || attempts.Load() != 2 {
					t.Fatalf("retry result err=%v at=%q calls=%d", result.err, result.at, attempts.Load())
				}
				if !cachedImage || cached.Path != imagePath || cached.Preview != "Obraz: A complete image caption." || !cached.Visual || !cached.AI {
					t.Fatalf("cache contains incomplete/wrong image note: %+v", cached)
				}
				if len(result.results) != 1 || result.results[0].VisualIndexed != 1 || result.results[0].AnalysisFailed != 0 || result.results[0].SkippedTotal != 0 || result.results[0].indexPreview[imagePath] != cached.Preview {
					t.Fatalf("successful retried image was skipped or mismatched: %+v", result.results)
				}
				firstRequest, secondRequest := <-requests, <-requests
				firstDeadline, secondDeadline := <-deadlines, <-deadlines
				if firstDeadline != secondDeadline {
					t.Fatal("retry extended the original image-analysis deadline")
				}
				if firstRequest != secondRequest {
					t.Fatal("retry changed the image/message payload")
				}
			}
			if len(wrapperStats) == 0 || len(wrapperStats) != len(outerStats) {
				t.Fatalf("existing metrics sink lost: wrapper=%d outer=%d", len(wrapperStats), len(outerStats))
			}
			for len(wrapperStats) > 0 {
				wrapped, outer := <-wrapperStats, <-outerStats
				if !reflect.DeepEqual(wrapped, outer) {
					t.Fatalf("metrics sink was replaced or changed: wrapper=%+v outer=%+v", wrapped, outer)
				}
			}
		})
	}
}

func TestDescribeIndexedImageDoesNotRetryProviderErrors(t *testing.T) {
	for _, providerErr := range []error{context.Canceled, errors.New("provider failed")} {
		t.Run(providerErr.Error(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var calls atomic.Int32
			provider := llm.Metered(imageIndexProviderFunc{complete: func(context.Context, []llm.Message, []llm.ToolDef) (<-chan llm.Delta, error) {
				if calls.Add(1) > 1 {
					cancel()
				} // Bound a regressed retry instead of spinning.
				return nil, providerErr
			}}, "test", "main", func(llm.CallStat) {})
			caption, err := describeIndexedImage(ctx, provider, writeImageIndexFixture(t), "en")
			if !errors.Is(err, providerErr) || caption != "" || calls.Load() != 1 {
				t.Fatalf("provider failure retried/accepted: caption=%q err=%v calls=%d", caption, err, calls.Load())
			}
		})
	}
}
