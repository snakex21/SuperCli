package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"supercli/internal/system/uilang"
)

func TestReferencedInterfaceMessagesExist(t *testing.T) {
	checked := map[string]bool{}
	for _, directory := range []string{".", "../../app", "../../llm"} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), directory+"/"+entry.Name(), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				key, err := strconv.Unquote(literal.Value)
				if err != nil || (!strings.HasPrefix(key, "tui.") && !strings.HasPrefix(key, "update.") && !strings.HasPrefix(key, "app.")) || strings.HasSuffix(key, ".") {
					return true
				}
				checked[key] = true
				if uilang.Text("en", key) == key {
					t.Errorf("missing catalog key %q referenced in %s", key, entry.Name())
				}
				return true
			})
		}
	}
	if len(checked) < 500 {
		t.Fatalf("unexpectedly few UI message references: %d", len(checked))
	}
}
