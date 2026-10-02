package llm

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const schemaResidencySmall = `{"type":"object","properties":{"file":{"type":"string"},"from":{"type":"integer"},"to":{"type":"integer"}},"required":["file","from","to"]}`

func resetSchemaResidencyCache() {
	normalizedToolSchemaCache.Lock()
	normalizedToolSchemaCache.entries = make(map[toolSchemaCacheKey]json.RawMessage)
	normalizedToolSchemaCache.order = nil
	normalizedToolSchemaCache.bytes = 0
	normalizedToolSchemaCache.Unlock()
}

func schemaResidencyPayload() (entries, payload int) {
	normalizedToolSchemaCache.Lock()
	defer normalizedToolSchemaCache.Unlock()
	for key, value := range normalizedToolSchemaCache.entries {
		entries++
		payload += len(key.raw) + len(value)
	}
	return entries, payload
}

func schemaResidencyLarge(id, size int) string {
	return fmt.Sprintf(`{"type":"object","description":"%s","properties":{"id%d":{"type":"string"}}}`, strings.Repeat("d", size), id)
}

// Both sides of the comparison read and compile the same real source literals.
// Dynamic schemas and user/MCP schemas are intentionally excluded from this
// builtin-footprint measurement.
func schemaResidencyBuiltins(tb testing.TB) []string {
	tb.Helper()
	seen := make(map[string]bool)
	var schemas []string
	for _, root := range []string{"../tools", "../agent"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				pair, ok := n.(*ast.KeyValueExpr)
				if !ok {
					return true
				}
				key, ok := pair.Key.(*ast.Ident)
				if !ok || key.Name != "Schema" {
					return true
				}
				literal, ok := pair.Value.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				raw, err := strconv.Unquote(literal.Value)
				if err == nil && !seen[raw] {
					seen[raw] = true
					schemas = append(schemas, raw)
				}
				return true
			})
			return nil
		})
		if err != nil {
			tb.Fatal(err)
		}
	}
	if len(schemas) == 0 {
		tb.Fatal("no builtin schemas found")
	}
	return schemas
}

func TestSchemaResidencyBuiltinFootprint(t *testing.T) {
	resetSchemaResidencyCache()
	t.Cleanup(resetSchemaResidencyCache)
	schemas := schemaResidencyBuiltins(t)
	largest := 0
	for _, raw := range schemas {
		if len(raw) > largest {
			largest = len(raw)
		}
		if _, err := CompileToolSchema(raw); err != nil {
			t.Fatal(err)
		}
	}
	entries, payload := schemaResidencyPayload()
	t.Logf("unique static builtin schemas=%d, both-mode entries=%d, payload=%d B, largest raw=%d B", len(schemas), entries, payload, largest)
}

func BenchmarkSchemaResidencyHot(b *testing.B) {
	resetSchemaResidencyCache()
	if _, err := normalizeOpenAIToolSchemaChecked(schemaResidencySmall); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(schemaResidencySmall)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := normalizeOpenAIToolSchemaChecked(schemaResidencySmall); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSchemaResidencyCold(b *testing.B) {
	for _, size := range []int{0, 32 << 10} {
		b.Run(fmt.Sprintf("description-%d", size), func(b *testing.B) {
			resetSchemaResidencyCache()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				raw := schemaResidencyLarge(i, size)
				if _, err := CompileToolSchema(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Use -benchtime=1x so every iteration starts from an empty cache and the
// measured work is cold compilation, rather than a repeated warm workload.
func BenchmarkSchemaResidencyColdChurn(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resetSchemaResidencyCache()
		for j := 0; j < 100; j++ {
			if _, err := CompileToolSchema(schemaResidencyLarge(j, 256<<10)); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkSchemaResidencyRetainedHeap(b *testing.B) {
	var retained, payload, entries int
	for i := 0; i < b.N; i++ {
		resetSchemaResidencyCache()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for j := 0; j < 100; j++ {
			if _, err := CompileToolSchema(schemaResidencyLarge(j, 256<<10)); err != nil {
				b.Fatal(err)
			}
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		retained = int(after.HeapAlloc) - int(before.HeapAlloc)
		entries, payload = schemaResidencyPayload()
	}
	b.ReportMetric(float64(retained), "retained-B")
	b.ReportMetric(float64(payload), "cache-payload-B")
	b.ReportMetric(float64(entries), "cache-entries")
}

func BenchmarkSchemaResidencyHotLargestBuiltin(b *testing.B) {
	schemas := schemaResidencyBuiltins(b)
	raw := schemas[0]
	for _, schema := range schemas[1:] {
		if len(schema) > len(raw) {
			raw = schema
		}
	}
	resetSchemaResidencyCache()
	if _, err := normalizeOpenAIToolSchemaChecked(raw); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := normalizeOpenAIToolSchemaChecked(raw); err != nil {
			b.Fatal(err)
		}
	}
}
