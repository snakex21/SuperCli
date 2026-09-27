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

func TestReadContextMissingLineUsesBoundedStart(t *testing.T) {
	root := t.TempDir()
	var source strings.Builder
	for line := 1; line <= 700; line++ {
		fmt.Fprintf(&source, "fixture_line_%03d\n", line)
	}
	if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte(source.String()), 0600); err != nil {
		t.Fatal(err)
	}
	reg := core.NewRegistry()
	reg.MustRegister(NewReadContext(root).Spec())
	for _, tc := range []struct {
		name, args  string
		first, last int
	}{
		{"recorded-radius", `{"file":"source.txt","radius":20}`, 1, 21},
		{"default-radius", `{"file":"source.txt"}`, 1, 11},
		{"oversized-radius", `{"file":"source.txt","radius":100000}`, 1, 251},
		{"explicit-center", `{"file":"source.txt","line":40,"radius":3}`, 37, 43},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := reg.Execute(context.Background(), "read_context", json.RawMessage(tc.args))
			if err != nil || got.Err != nil {
				t.Fatalf("read: %v %v", err, got.Err)
			}
			if n := strings.Count(got.Text, " | "); n != tc.last-tc.first+1 {
				t.Fatalf("read %d lines, wanted %d-%d", n, tc.first, tc.last)
			}
			for _, line := range []int{tc.first, tc.last} {
				if !strings.Contains(got.Text, fmt.Sprintf(" | fixture_line_%03d\n", line)) {
					t.Fatalf("missing boundary line %d: %s", line, got.Text)
				}
			}
			if strings.Contains(got.Text, fmt.Sprintf("fixture_line_%03d", tc.last+1)) || strings.Contains(got.Text, "end of file") {
				t.Fatal("read crossed the bounded window or invented EOF")
			}
		})
	}
}

func TestReadContextMissingLineMatchesRecordedFallbackOnShortFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "service"), 0700); err != nil {
		t.Fatal(err)
	}
	source := "package service\n\nfunc Publish() {\n\t// verified fixture body\n}\n"
	if err := os.WriteFile(filepath.Join(root, "service", "publish.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	reg := core.NewRegistry()
	reg.MustRegister(NewReadContext(root).Spec())
	reg.MustRegister(NewReadLines(root).Spec())
	got, err := reg.Execute(context.Background(), "read_context", json.RawMessage(`{"file":"service/publish.go","radius":20}`))
	if err != nil || got.Err != nil {
		t.Fatalf("recorded call: %v %v", err, got.Err)
	}
	fallback, err := reg.Execute(context.Background(), "read_lines", json.RawMessage(`{"file":"service/publish.go"}`))
	if err != nil || fallback.Err != nil {
		t.Fatalf("recorded retry: %v %v", err, fallback.Err)
	}
	if got.Text != fallback.Text || !strings.Contains(got.Text, "end of file at line 5") {
		t.Fatalf("first call did not return the evidence obtained by the recorded retry: %s", got.Text)
	}
}

func TestReadContextDefaultPreservesInvalidCentersAndReadErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "valid.txt"), []byte("first\nsecond\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := core.NewRegistry()
	reg.MustRegister(NewReadContext(root).Spec())
	for _, raw := range []string{
		`{"file":"valid.txt","line":0}`,
		`{"file":"valid.txt","line":-1}`,
		`{"file":"valid.txt","line":100}`,
		`{"file":"valid.txt","line":"invalid"}`,
		`{"file":"missing.txt"}`,
		`{"file":"../outside.txt"}`,
	} {
		got, err := reg.Execute(context.Background(), "read_context", json.RawMessage(raw))
		if err == nil && got.Err == nil {
			t.Errorf("invalid call succeeded: %s", raw)
		}
		if strings.Contains(got.Text, " | ") {
			t.Errorf("invalid call returned file contents: %s", raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := reg.Execute(ctx, "read_context", json.RawMessage(`{"file":"valid.txt"}`))
	if !errors.Is(err, context.Canceled) && !errors.Is(got.Err, context.Canceled) {
		t.Fatalf("cancellation lost: %v %v", err, got.Err)
	}
}
