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

func TestReadManyRegistryAcceptsUnambiguousRangeLists(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("one\ntwo\nthree\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reg := core.NewRegistry()
	reg.MustRegister(NewReadMany(root).Spec())
	ranges := []string{"b.go:2-3", "a.go:1-2", "b.go:1-1"}
	expectedArgs, _ := json.Marshal(map[string]any{"reads": strings.Join(ranges, " | ")})
	expected, err := reg.Execute(context.Background(), "read_many", expectedArgs)
	if err != nil || expected.Err != nil {
		t.Fatalf("canonical: %v / %v", err, expected.Err)
	}
	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprint(wrapped), func(t *testing.T) {
			var reads any = ranges
			if wrapped {
				reads = map[string]any{"item": ranges}
			}
			args, _ := json.Marshal(map[string]any{"reads": reads})
			before := string(args)
			result, err := reg.Execute(context.Background(), "read_many", args)
			if err != nil || result.Err != nil {
				t.Fatalf("range list rejected: %v / %v", err, result.Err)
			}
			if result.Text != expected.Text {
				t.Fatalf("ranges/order changed:\n%s\nwant:\n%s", result.Text, expected.Text)
			}
			if string(args) != before {
				t.Fatal("original call arguments mutated")
			}
		})
	}
}

func TestReadManyRepairRejectsAmbiguousListsBeforeDispatch(t *testing.T) {
	for _, tc := range []struct{ name, args string }{
		{"duplicate reads", "{\"reads\":[\"a.go:1-2\"],\"reads\":[\"b.go:1-2\"]}"},
		{"duplicate item", "{\"reads\":{\"item\":[\"a.go:1-2\"],\"item\":[\"b.go:1-2\"]}}"},
		{"extra wrapper field", "{\"reads\":{\"item\":[\"a.go:1-2\"],\"limit\":1}}"},
		{"extra root field", "{\"reads\":[\"a.go:1-2\"],\"limit\":1}"},
		{"scalar wrapper", "{\"reads\":{\"item\":\"a.go:1-2\"}}"},
		{"structured entries", "{\"reads\":[{\"file\":\"a.go\",\"from\":1,\"to\":2}]}"},
		{"mixed entries", "{\"reads\":[\"a.go:1-2\",1]}"},
		{"empty list", "{\"reads\":[]}"},
		{"null list", "{\"reads\":null}"},
		{"null entry", "{\"reads\":[\"a.go:1-2\",null]}"},
		{"blank entry", "{\"reads\":[\"a.go:1-2\",\" \"]}"},
		{"delimiter in entry", "{\"reads\":[\"a.go:1-2 | b.go:1-2\"]}"},
		{"too many ranges", "{\"reads\":[\"a.go:1-2\",\"a.go:1-2\",\"a.go:1-2\",\"a.go:1-2\",\"a.go:1-2\",\"a.go:1-2\",\"a.go:1-2\",\"a.go:1-2\",\"a.go:1-2\",\"a.go:1-2\",\"a.go:1-2\",\"a.go:1-2\",\"a.go:1-2\"]}"},
		{"trailing JSON", "{\"reads\":[\"a.go:1-2\"]} {}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := json.RawMessage(tc.args)
			before := string(args)
			if _, ok := repairReadManyRangeList(args); ok {
				t.Fatal("ambiguous list accepted for repair")
			}
			calls := 0
			spec := NewReadMany(t.TempDir()).Spec()
			spec.Fn = func(context.Context, json.RawMessage) (core.Result, error) {
				calls++
				return core.Result{}, nil
			}
			reg := core.NewRegistry()
			reg.MustRegister(spec)
			result, err := reg.Execute(context.Background(), "read_many", args)
			if err != nil || !errors.Is(result.Err, core.ErrInvalidToolArgs) || calls != 0 {
				t.Fatalf("unexpected dispatch: calls=%d err=%v result=%+v", calls, err, result)
			}
			if string(args) != before {
				t.Fatal("original arguments mutated")
			}
		})
	}
}
