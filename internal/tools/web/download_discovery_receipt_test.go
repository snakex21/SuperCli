package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/core"
	"supercli/internal/tools/search"
)

// This exercises the actual registered tool functions and real saved files.
// Only HTTP transport is a fixture; the agent-loop lifetime and invalidation
// boundaries are covered by completed_operations_discovery_test in agent.
func TestWebDownloadDiscoveryKeepsCheckedReceiptAndIndependentDestinations(t *testing.T) {
	for _, sameURL := range []bool{false, true} {
		name := "different-url-and-file"
		if sameURL {
			name = "same-url-new-file"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			tool := NewWebDownload(t.TempDir())
			transfers := 0
			firstBody, secondBody := []byte("first fixture\x00"), []byte("second fixture\x00")
			tool.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				transfers++
				body := firstBody
				if req.URL.Path == "/second.bin" {
					body = secondBody
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/octet-stream"}},
					ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body)), Request: req}, nil
			})}
			spec := tool.Spec()
			registry := core.NewRegistry()
			registry.MustRegister(spec)
			registry.MustRegister(core.Tool{Name: "inspect_fixture", Description: "Inspect a saved fixture", ReadOnly: true,
				Schema: `{"type":"object"}`, Fn: func(context.Context, json.RawMessage) (core.Result, error) {
					return core.Result{Text: "fixture inspected"}, nil
				}})
			discovery := search.NewToolSearcher(registry, nil).Spec()
			registry.MustRegister(discovery)
			if discovery.ReadOnly || !discovery.PreservesEvidence || spec.ReplaySuccess == nil {
				t.Fatal("registered contracts lost separate discovery and checked-effect capabilities")
			}
			args, _ := json.Marshal(webDownloadArgs{URL: "https://assets.example.test/first.bin", Path: "assets/first.bin"})
			prior, err := spec.Fn(ctx, args)
			if err != nil || prior.Err != nil || prior.Operation == nil || transfers != 1 {
				t.Fatalf("actual initial download failed: %+v %v transfers=%d", prior, err, transfers)
			}
			if found, err := discovery.Fn(ctx, json.RawMessage(`{"query":"inspect_fixture"}`)); err != nil || found.Err != nil || !registry.IsActive("inspect_fixture") {
				t.Fatalf("standard discovery failed: %+v %v", found, err)
			}
			replayed, handled := spec.ReplaySuccess(ctx, args, prior)
			if !handled || replayed.Err != nil || !replayed.Inert || transfers != 1 || !strings.Contains(replayed.Text, "Download already complete and verified") {
				t.Fatalf("discovery lost the actual receipt: %+v handled=%v transfers=%d", replayed, handled, transfers)
			}
			secondURL, expected := "https://assets.example.test/second.bin", secondBody
			if sameURL {
				secondURL, expected = "https://assets.example.test/first.bin", firstBody
			}
			secondArgs, _ := json.Marshal(webDownloadArgs{URL: secondURL, Path: "assets/second.bin"})
			// The dispatcher never supplies a receipt from another argument key.
			// Even when given one directly, the real callback must not report the
			// new destination as complete (it may reject the foreign path).
			if replayed, handled := spec.ReplaySuccess(ctx, secondArgs, prior); replayed.Operation != nil || replayed.Inert || (handled && replayed.Err == nil) || transfers != 1 {
				t.Fatal("a different destination was incorrectly treated as completed")
			}
			second, err := spec.Fn(ctx, secondArgs)
			if err != nil || second.Err != nil || second.Inert || second.Operation == nil || transfers != 2 {
				t.Fatalf("independent download did not execute: %+v %v transfers=%d", second, err, transfers)
			}
			for filename, wanted := range map[string][]byte{"first.bin": firstBody, "second.bin": expected} {
				saved, err := os.ReadFile(filepath.Join(tool.BaseDir, "assets", filename))
				if err != nil || !bytes.Equal(saved, wanted) {
					t.Fatalf("saved content changed: %s %v", filename, err)
				}
			}
			// A metadata search cannot turn a changed output into a valid receipt.
			if err := os.WriteFile(filepath.Join(tool.BaseDir, "assets", "first.bin"), []byte("changed\x00"), 0600); err != nil {
				t.Fatal(err)
			}
			if found, err := discovery.Fn(ctx, json.RawMessage(`{"query":"inspect_fixture"}`)); err != nil || found.Err != nil {
				t.Fatalf("fresh discovery after modification failed: %+v %v", found, err)
			}
			if changed, handled := spec.ReplaySuccess(ctx, args, prior); !handled || changed.Err == nil || changed.Inert || transfers != 2 {
				t.Fatal("discovery bypassed the real saved-file state check")
			}
			t.Logf("duplicate_transfers=0 total_transfers=%d independent_files=2", transfers)
		})
	}
}
