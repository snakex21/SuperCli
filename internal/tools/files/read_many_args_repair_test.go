package files

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	spec := NewReadMany(root).Spec()
	handler, calls := spec.Fn, 0
	spec.Fn = func(ctx context.Context, args json.RawMessage) (Result, error) {
		calls++
		return handler(ctx, args)
	}
	reg.MustRegister(spec)
	ranges := []string{"b.go:2-3", "a.go:1-2", "b.go:1-1"}
	expectedArgs, _ := json.Marshal(map[string]any{"reads": strings.Join(ranges, " | ")})
	expected, err := reg.Execute(context.Background(), "read_many", expectedArgs)
	if err != nil || expected.Err != nil {
		t.Fatalf("canonical: %v / %v", err, expected.Err)
	}
	if strings.Count(expected.Text, "one") != 2 || strings.Count(expected.Text, "two") != 2 || strings.Count(expected.Text, "three") != 1 {
		t.Fatal("canonical fixture returned unexpected lines")
	}
	for _, tc := range []struct {
		name  string
		reads any
	}{
		{"list", ranges},
		{"wrapped list", map[string]any{"item": ranges}},
		{"wrapped scalar", map[string]any{"item": strings.Join(ranges, " | ")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls = 0
			args, _ := json.Marshal(map[string]any{"reads": tc.reads})
			before := string(args)
			result, err := reg.Execute(context.Background(), "read_many", args)
			if err != nil || result.Err != nil {
				t.Fatalf("range list rejected: %v / %v", err, result.Err)
			}
			if calls != 1 || !reflect.DeepEqual(result, expected) {
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
		{"unknown scalar wrapper", "{\"reads\":{\"items\":\"a.go:1-2\"}}"},
		{"duplicate scalar reads", "{\"reads\":{\"item\":\"a.go:1-2\"},\"reads\":{\"item\":\"b.go:1-2\"}}"},
		{"duplicate scalar item", "{\"reads\":{\"item\":\"a.go:1-2\",\"item\":\"b.go:1-2\"}}"},
		{"extra scalar wrapper field", "{\"reads\":{\"item\":\"a.go:1-2\",\"limit\":1}}"},
		{"extra scalar root field", "{\"reads\":{\"item\":\"a.go:1-2\"},\"limit\":1}"},
		{"blank scalar", `{"reads":{"item":" \n\t "}}`},
		{"empty scalar", "{\"reads\":{\"item\":\"\"}}"},
		{"null scalar", "{\"reads\":{\"item\":null}}"},
		{"number scalar", "{\"reads\":{\"item\":17}}"},
		{"boolean scalar", "{\"reads\":{\"item\":true}}"},
		{"object scalar", "{\"reads\":{\"item\":{\"file\":\"a.go\"}}}"},
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

func TestReadManyScalarRepairPreservesCanonicalProjection(t *testing.T) {
	for _, value := range []string{
		"a.go:3-3 | b.go:1-2 | a.go:1-1",
		"C:\\folder\\a.go:2-3",
		"folder with spaces/ą.go:1-2 | other.go:5-6",
		"\"quoted folder/a.go\":2-3",
		"\"pipe|name.go\":2-3",
		"a.go:1-2 | bare.go",
		"  a.go:1-1  ",
	} {
		raw, _ := json.Marshal(map[string]any{"reads": map[string]any{"item": value}})
		original := string(raw)
		fixed, ok := repairReadManyRangeList(raw)
		if !ok {
			t.Fatal("unambiguous scalar wrapper not repaired")
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(fixed, &object); err != nil {
			t.Fatal(err)
		}
		var preserved string
		if json.Unmarshal(object["reads"], &preserved) != nil || preserved != value {
			t.Fatal("literal scalar changed")
		}
		canonical, _ := json.Marshal(value)
		before, bErr := decodeReadManyRequests(canonical)
		after, aErr := decodeReadManyRequests(object["reads"])
		if bErr != nil || aErr != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("canonical request projection changed")
		}
		if _, again := repairReadManyRangeList(fixed); again {
			t.Fatal("repair is not idempotent")
		}
		if string(raw) != original {
			t.Fatal("original call arguments mutated")
		}
	}
}

func BenchmarkReadManyScalarArgumentRepair(b *testing.B) {
	for _, kind := range []string{"canonical", "wrapper", "wrapped-list"} {
		b.Run(kind, func(b *testing.B) {
			spec := NewReadMany(".").Spec()
			spec.Fn = func(context.Context, json.RawMessage) (Result, error) { return Result{Text: "fixture validated"}, nil }
			reg := core.NewRegistry()
			reg.MustRegister(spec)
			raw := json.RawMessage("{\"reads\":\"a.go:3-3 | b.go:1-2 | a.go:1-1\"}")
			switch kind {
			case "wrapper":
				raw = json.RawMessage("{\"reads\":{\"item\":\"a.go:3-3 | b.go:1-2 | a.go:1-1\"}}")
			case "wrapped-list":
				raw = json.RawMessage("{\"reads\":{\"item\":[\"a.go:3-3\",\"b.go:1-2\",\"a.go:1-1\"]}}")
			}
			b.ReportAllocs()
			for b.Loop() {
				_, _ = reg.Execute(context.Background(), "read_many", raw)
			}
		})
	}
}

func TestReadManyRepairJSONWhitespace(t *testing.T) {
	for _, tc := range []struct {
		name, item, canonical string
		kind                  byte
	}{
		{"scalar", "\"  a.go:3-3 | a.go:1-1  \"", "  a.go:3-3 | a.go:1-1  ", '"'},
		{"list", "[ \"a.go:3-3\", \"a.go:1-1\" ]", "a.go:3-3 | a.go:1-1", '['},
	} {
		t.Run(tc.name, func(t *testing.T) {
			whitespace := " \t\r\n"
			raw := json.RawMessage(whitespace + "{" + whitespace + "\"reads\":" + whitespace + "{" + whitespace + "\"item\":" + whitespace + tc.item + whitespace + "}" + whitespace + "}" + whitespace)
			before := string(raw)
			root, ok := uniqueArgumentObject(raw)
			if !ok {
				t.Fatal("valid whitespace rejected")
			}
			wrapper, ok := uniqueArgumentObject(root["reads"])
			if !ok {
				t.Fatal("valid wrapper whitespace rejected")
			}
			if len(wrapper["item"]) == 0 || wrapper["item"][0] != tc.kind {
				t.Fatal("RawMessage didn't start at JSON value")
			}
			fixed, ok := repairReadManyRangeList(raw)
			if !ok {
				t.Fatal("whitespace prevented repair")
			}
			var args struct{ Reads string }
			if err := json.Unmarshal(fixed, &args); err != nil || args.Reads != tc.canonical {
				t.Fatal("whitespace changed scalar/range content")
			}
			if string(raw) != before {
				t.Fatal("source changed")
			}
		})
	}
}
