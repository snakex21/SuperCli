package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func TestSchemaCacheMatchesUncachedCompilerAndPreparation(t *testing.T) {
	tests := []struct {
		name   string
		schema string
		args   []string
	}{
		{"empty", "", []string{"", "not-json", `{}`, `null`}},
		{"whitespace", " \t\n", []string{"", "not-json", `{}`}},
		{"true", "true", []string{`{}`, `null`, `7`, ""}},
		{"false", "false", []string{`{}`, `null`, `7`, ""}},
		{"unconstrained", "{}", []string{`{}`, `{"future":true}`, ""}},
		{"shorthand", `{"count":{"type":"integer"},"path":{"type":"string"}}`, []string{`{"count":"2","path":"a.txt"}`, `{"count":2,"unknown":true}`, `{"count":"bad"}`}},
		{"nested and coercion", `{"type":"object","properties":{"count":{"type":"integer","minimum":1,"maximum":3},"ratio":{"type":"number","minimum":0.1,"maximum":3.5},"enabled":{"type":"boolean"},"argv":{"type":"array","items":{"type":"string"}},"kind":{"enum":["fact","decision"]},"nested":{"type":"object","properties":{"x":{"const":"ok"}},"required":["x"],"additionalProperties":false}},"required":["count","kind"]}`, []string{`{"count":"2","ratio":"1.2","enabled":"true","argv":"[\"-h\"]","kind":"fact","nested":{"x":"ok"}}`, `{"count":4,"kind":"fact"}`, `{"count":2,"kind":"unknown"}`, `{"count":2,"kind":"fact","nested":{"x":"no"}}`, `{"count":2,"kind":"fact","unknown":true}`, "not-json"}},
		{"explicit open envelope", `{"type":"object","properties":{"count":{"type":"integer"}},"additionalProperties":true}`, []string{`{"count":"2","extra":true}`, `{"count":"no"}`}},
		{"unsupported patternProperties", `{"type":"object","patternProperties":{"^x-":{"type":"string"}},"additionalProperties":false}`, []string{`{"x-future":7}`, `{}`}},
		{"combinator envelope", `{"type":"object","properties":{"base":{"type":"string"}},"anyOf":[{"properties":{"left":{"type":"integer"}}},{"properties":{"right":{"type":"string"}}}]}`, []string{`{"left":2}`, `{"base":"b","right":"r"}`, `{"left":"bad","right":8}`}},
		{"regex bypass", `{"type":"object","properties":{"tag":{"type":"string","pattern":"^[a-z]+$"}}}`, []string{`{"tag":"ok"}`, `{"tag":"A1"}`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cache := newCompiledSchemaCache(8, schemaCacheMaxBytes)
			oracle, oracleErr := compileToolSchema(test.schema)
			cached, cachedErr := cache.compile(test.schema)
			if schemaCacheErrorText(oracleErr) != schemaCacheErrorText(cachedErr) || !reflect.DeepEqual(oracle, cached) {
				t.Fatalf("compilation differs: cached=%v oracle=%v", cachedErr, oracleErr)
			}
			tool := Tool{Name: "fixture"}
			for _, raw := range test.args {
				want, wantErr := prepareRegisteredArgs(tool, oracle, json.RawMessage(raw))
				got, gotErr := prepareRegisteredArgs(tool, cached, json.RawMessage(raw))
				if string(got) != string(want) || schemaCacheErrorText(gotErr) != schemaCacheErrorText(wantErr) ||
					errors.Is(gotErr, ErrInvalidToolArgs) != errors.Is(wantErr, ErrInvalidToolArgs) {
					t.Fatalf("args %q: got %q, %v; want %q, %v", raw, got, gotErr, want, wantErr)
				}
			}
			// A genuinely nil RawMessage also keeps the old fail-open/error behavior.
			want, wantErr := prepareRegisteredArgs(tool, oracle, nil)
			got, gotErr := prepareRegisteredArgs(tool, cached, nil)
			if !reflect.DeepEqual(got, want) || schemaCacheErrorText(gotErr) != schemaCacheErrorText(wantErr) {
				t.Fatalf("nil args changed: %v / %v", gotErr, wantErr)
			}
			requireSchemaCacheBounds(t, cache)
		})
	}
}

func TestSchemaCacheKeysExactSourceAndDoesNotRetainInvalidPlans(t *testing.T) {
	cache := newCompiledSchemaCache(16, schemaCacheMaxBytes)
	source := `{"type":"object","properties":{"count":{"type":"integer","maximum":2}}}`
	first, err := cache.compile(source)
	if err != nil || first == nil {
		t.Fatal(err)
	}
	again, err := cache.compile(source)
	if err != nil || again != first {
		t.Fatal("identical source did not share its immutable plan")
	}
	spaced, err := cache.compile(" " + source)
	if err != nil || spaced == first || !reflect.DeepEqual(spaced, first) {
		t.Fatal("source key was normalized or semantics changed")
	}
	changed, err := cache.compile(strings.Replace(source, `"maximum":2`, `"maximum":3`, 1))
	if err != nil || changed == first || first.validateJSON(json.RawMessage(`{"count":3}`)) == nil || changed.validateJSON(json.RawMessage(`{"count":3}`)) != nil {
		t.Fatal("changed contract used an older validation plan")
	}
	// Exercise a first insertion, not a hit: its short source is backed by a
	// large caller allocation. Map and FIFO must share one independent clone.
	uniqueSource := strings.Replace(source, `"maximum":2`, `"maximum":1`, 1) + " \n"
	backing := strings.Repeat("padding", 128<<10) + uniqueSource
	substring := backing[len(backing)-len(uniqueSource):]
	fromSubstring, err := cache.compile(substring)
	if err != nil || fromSubstring == nil {
		t.Fatal("substring source changed compilation")
	}
	var ownedKey string
	for key, entry := range cache.entries {
		if key == substring {
			ownedKey = key
			if entry.plan != fromSubstring || unsafe.StringData(key) == unsafe.StringData(substring) {
				t.Fatal("map key retained the caller's backing allocation")
			}
		}
	}
	if ownedKey == "" || ownedKey != uniqueSource {
		t.Fatal("owned key lost exact source bytes")
	}
	foundFIFO := false
	for _, key := range cache.order {
		if key == ownedKey {
			foundFIFO = true
			if unsafe.StringData(key) != unsafe.StringData(ownedKey) {
				t.Fatal("FIFO used a separate or caller-owned key")
			}
		}
	}
	if !foundFIFO {
		t.Fatal("owned key missing from FIFO")
	}
	before := len(cache.entries)
	for _, raw := range []string{"", " \n\t", "{", "[]", "null", `{"type":"future-type"}`, `{"properties":[]}`, `{"type":"string","pattern":"["}`, `{"minimum":1e10001}`} {
		for range 2 {
			want, wantErr := compileToolSchema(raw)
			got, gotErr := cache.compile(raw)
			if schemaCacheErrorText(gotErr) != schemaCacheErrorText(wantErr) || !reflect.DeepEqual(got, want) {
				t.Fatalf("source %q changed error/fail-open behavior: %v / %v", raw, gotErr, wantErr)
			}
		}
		if len(cache.entries) != before {
			t.Fatalf("invalid or nil plan retained for %q", raw)
		}
	}
	for _, name := range []string{"first_tool", "second_tool"} {
		reg := NewRegistry()
		tool := Tool{Name: name, Description: "invalid fixture", Schema: `{"type":"future-type"}`, Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }}
		_, oracleErr := compileToolSchema(tool.Schema)
		err := reg.Register(tool)
		if err == nil || err.Error() != fmt.Sprintf("register %q: %s", name, oracleErr) || reg.Len() != 0 {
			t.Fatalf("registration error changed or installed a tool: %v", err)
		}
	}
	requireSchemaCacheBounds(t, cache)
}

func TestSchemaCacheRegistryCallbacksPermissionsAndRootsRemainIndependent(t *testing.T) {
	schema := `{"$comment":"schema-cache-independent-registries","type":"object","properties":{"count":{"type":"integer","minimum":1,"maximum":3},"path":{"type":"string"}},"required":["count","path"]}`
	a, b := NewRegistry(), NewRegistry()
	allowedA := true
	permissionErr := errors.New("project A permission revoked")
	repairsA, repairsB := 0, 0
	a.MustRegister(Tool{Name: "operate", Description: "project A", Schema: schema, ReadOnly: true,
		RepairArgs: func(json.RawMessage) (json.RawMessage, bool) {
			repairsA++
			return json.RawMessage(`{"count":2,"path":"a.txt"}`), true
		},
		Fn: func(_ context.Context, args json.RawMessage) (Result, error) {
			if !allowedA {
				return Result{Err: permissionErr}, nil
			}
			return Result{Text: "project-A/" + string(args)}, nil
		},
	})
	b.MustRegister(Tool{Name: "operate", Description: "project B", Schema: schema,
		RepairArgs: func(json.RawMessage) (json.RawMessage, bool) {
			repairsB++
			return json.RawMessage(`{"count":1,"path":"b.txt"}`), true
		},
		Fn: func(_ context.Context, args json.RawMessage) (Result, error) {
			return Result{Text: "project-B/" + string(args)}, nil
		},
	})
	if a.schemas["operate"] == nil || a.schemas["operate"] != b.schemas["operate"] {
		t.Fatal("registrations did not share the eligible immutable plan")
	}
	a.ActivateDiscovered("operate")
	if b.IsVisible("operate") || b.IsActive("operate") || b.tools["operate"].ReadOnly || !a.tools["operate"].ReadOnly {
		t.Fatal("plan reuse shared tool flags or activation")
	}
	for _, fixture := range []struct {
		reg                 *Registry
		prefix, path, count string
	}{
		{a, "project-A/", "a.txt", `"count":2`},
		{b, "project-B/", "b.txt", `"count":1`},
	} {
		result, err := fixture.reg.Execute(context.Background(), "operate", json.RawMessage(`{"count":"bad","path":"original.txt"}`))
		if err != nil || result.Err != nil || !strings.HasPrefix(result.Text, fixture.prefix) || !strings.Contains(result.Text, fixture.path) || !strings.Contains(result.Text, fixture.count) {
			t.Fatalf("registry-local repair or callback changed: %s %v %v", result.Text, err, result.Err)
		}
	}
	if repairsA != 1 || repairsB != 1 {
		t.Fatalf("repair callbacks crossed registries: A=%d B=%d", repairsA, repairsB)
	}
	allowedA = false
	result, err := a.Execute(context.Background(), "operate", json.RawMessage(`{"count":2,"path":"a.txt"}`))
	if err != nil || !errors.Is(result.Err, permissionErr) {
		t.Fatalf("current permission was not checked by actual callback: %v %v", err, result.Err)
	}
	result, err = b.Execute(context.Background(), "operate", json.RawMessage(`{"count":2,"path":"b.txt"}`))
	if err != nil || result.Err != nil || !strings.HasPrefix(result.Text, "project-B/") {
		t.Fatal("permission state crossed registry boundary")
	}
}

func TestSchemaCacheEnsureReadOutputKeepsIndependentStores(t *testing.T) {
	a, b := NewRegistry(), NewRegistry()
	a.EnsureReadOutput()
	b.EnsureReadOutput()
	if a.schemas["read_output"] == nil || a.schemas["read_output"] != b.schemas["read_output"] || a.outputs == b.outputs {
		t.Fatal("read_output plan did not share or its stores were shared")
	}
	a.EnsureReadOutput()
	if a.Len() != 1 {
		t.Fatal("EnsureReadOutput repeated registration")
	}
	handleA, okA := a.outputs.put("project-A-output")
	handleB, okB := b.outputs.put("project-B-output")
	if !okA || !okB {
		t.Fatal("could not retain fixture outputs")
	}
	read := func(reg *Registry, handle string) Result {
		t.Helper()
		result, err := reg.Execute(context.Background(), "read_output", json.RawMessage(fmt.Sprintf(`{"handle":%q,"limit":"100"}`, handle)))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := read(a, handleA); result.Err != nil || !strings.Contains(result.Text, "project-A-output") {
		t.Fatalf("project A callback changed: %v %s", result.Err, result.Text)
	}
	if result := read(b, handleA); result.Err == nil {
		t.Fatal("project B resolved a foreign output handle")
	}
	if result := read(b, handleB); result.Err != nil || !strings.Contains(result.Text, "project-B-output") {
		t.Fatalf("project B callback changed: %v %s", result.Err, result.Text)
	}
}

func TestSchemaCacheConcurrentCompilationAndImmutableValidation(t *testing.T) {
	const source = `{"type":"object","properties":{"count":{"type":"integer","minimum":1,"maximum":3},"ratio":{"type":"number","minimum":0.1,"maximum":5.2},"enabled":{"type":"boolean"}},"required":["count","ratio"]}`
	cache := newCompiledSchemaCache(16, schemaCacheMaxBytes)
	oracle, err := compileToolSchema(source)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"count":"2","ratio":"1.5","enabled":"true"}`)
	want := coerceCompiledArgs(oracle, raw)
	start := make(chan struct{})
	plans := make(chan *compiledToolSchema, 24)
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			plan, err := cache.compile(source)
			if err != nil || plan == nil {
				t.Errorf("compile: %v", err)
				return
			}
			plans <- plan
			for range 20 {
				got := coerceCompiledArgs(plan, raw)
				if string(got) != string(want) || plan.validateJSON(got) != nil || plan.validateJSON(json.RawMessage(`{"count":9,"ratio":1.5}`)) == nil {
					t.Error("concurrent validation/coercion changed")
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(plans)
	var shared *compiledToolSchema
	count := 0
	for plan := range plans {
		count++
		if shared == nil {
			shared = plan
		}
		if plan != shared {
			t.Fatal("concurrent insertion returned different cached plans")
		}
	}
	if count != 24 || !reflect.DeepEqual(oracle, shared) {
		t.Fatal("shared plan was missing or mutated during validation")
	}
	requireSchemaCacheBounds(t, cache)
}

func TestSchemaCacheFIFOAndByteBounds(t *testing.T) {
	const sourceA, sourceB, sourceC = `{"const":"A"}`, `{"const":"B"}`, `{"const":"C"}`
	cache := newCompiledSchemaCache(2, schemaCacheMaxBytes)
	a, err := cache.compile(sourceA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cache.compile(sourceB)
	if err != nil {
		t.Fatal(err)
	}
	if hit, _ := cache.compile(sourceA); hit != a {
		t.Fatal("warm hit missed")
	}
	if _, err := cache.compile(sourceC); err != nil {
		t.Fatal(err)
	}
	if _, found := cache.entries[sourceA]; found {
		t.Fatal("hit promoted FIFO entry")
	}
	if hit, _ := cache.compile(sourceB); hit != b {
		t.Fatal("wrong FIFO entry evicted")
	}
	if nextA, _ := cache.compile(sourceA); nextA == a {
		t.Fatal("evicted plan remained cached")
	}
	requireSchemaCacheBounds(t, cache)

	oracle, err := compileToolSchema(sourceA)
	if err != nil {
		t.Fatal(err)
	}
	weight, eligible := compiledSchemaCacheWeight(sourceA, oracle)
	if !eligible {
		t.Fatal("small fixture is not eligible")
	}
	byteBounded := newCompiledSchemaCache(128, weight*2-1)
	for _, source := range []string{sourceA, sourceB, sourceC, sourceA, sourceC} {
		if _, err := byteBounded.compile(source); err != nil {
			t.Fatal(err)
		}
		requireSchemaCacheBounds(t, byteBounded)
		if len(byteBounded.entries) != 1 {
			t.Fatal("byte budget did not force eviction")
		}
	}
	tooSmall := newCompiledSchemaCache(8, weight-1)
	first, _ := tooSmall.compile(sourceA)
	second, _ := tooSmall.compile(sourceA)
	if first == second || len(tooSmall.entries) != 0 || tooSmall.bytes != 0 {
		t.Fatal("oversize entry retained")
	}
	for _, disabled := range []*compiledSchemaCache{newCompiledSchemaCache(0, schemaCacheMaxBytes), newCompiledSchemaCache(8, 0)} {
		first, firstErr := disabled.compile(sourceA)
		second, secondErr := disabled.compile(sourceA)
		if firstErr != nil || secondErr != nil || first == second || len(disabled.entries) != 0 {
			t.Fatal("disabled cache changed compilation or retained plans")
		}
	}
	churn := newCompiledSchemaCache(4, schemaCacheMaxBytes)
	for i := range 200 {
		if _, err := churn.compile(fmt.Sprintf(`{"const":%d}`, i)); err != nil {
			t.Fatal(err)
		}
		requireSchemaCacheBounds(t, churn)
	}
}

func TestSchemaCacheBypassesExpandedRegexNumericAndLargePlans(t *testing.T) {
	deep := "true"
	for range schemaCacheMaxDepth + 1 {
		deep = `{"items":` + deep + `}`
	}
	wide := `{"anyOf":[` + strings.Repeat("true,", schemaCacheMaxNodes) + "true]}"
	large := `{"type":"object","description":"` + strings.Repeat("x", schemaCacheMaxSourceBytes) + `"}`
	// Enum compilation preallocates a map using the original array length,
	// even when duplicate values leave only one visible key in the plan.
	duplicates := `{"type":[` + strings.Repeat(`"null",`, 599) + `"null"]}`
	if len(duplicates) > schemaCacheMaxSourceBytes {
		t.Fatal("duplicate fixture bypasses on source length")
	}
	for _, fixture := range []struct{ name, source string }{
		{"regex", `{"type":"object","properties":{"tag":{"type":"string","pattern":"^[a-z]+$"}}}`},
		{"large numeric bound", `{"minimum":1e1000}`},
		{"large numeric denominator", `{"maximum":1e-1000}`},
		{"expanded numeric enum", `{"enum":[1e9000]}`},
		{"duplicate array map hint", duplicates},
		{"deep", deep},
		{"wide", wide},
		{"large source", large},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			cache := newCompiledSchemaCache(16, schemaCacheMaxBytes)
			first, firstErr := cache.compile(fixture.source)
			second, secondErr := cache.compile(fixture.source)
			oracle, oracleErr := compileToolSchema(fixture.source)
			if firstErr != nil || secondErr != nil || oracleErr != nil || first == nil || second == first || !reflect.DeepEqual(first, oracle) || !reflect.DeepEqual(second, oracle) {
				t.Fatalf("bypass changed uncached compilation: %v %v %v", firstErr, secondErr, oracleErr)
			}
			if len(cache.entries) != 0 || cache.bytes != 0 {
				t.Fatal("ineligible plan entered cache")
			}
			requireSchemaCacheBounds(t, cache)
		})
	}
	// Normalization can shrink a number's BitLen without shrinking its backing
	// allocation. Account the backing word capacity as well as mathematical size.
	rational := new(big.Rat)
	words := make([]big.Word, 1, 1000)
	words[0] = 1
	rational.Num().SetBits(words)
	plan := &compiledToolSchema{root: &schemaNode{minimum: rational}}
	if rational.Num().BitLen() != 1 {
		t.Fatal("bad backing-capacity fixture")
	}
	if _, eligible := compiledSchemaCacheWeight("{}", plan); eligible {
		t.Fatal("large numeric backing allocation was ignored")
	}
	textPlan := &compiledToolSchema{root: &schemaNode{enumKeys: map[string]struct{}{
		strings.Repeat("a", schemaCacheMaxSourceBytes): {},
		strings.Repeat("b", schemaCacheMaxSourceBytes): {},
		strings.Repeat("c", schemaCacheMaxSourceBytes): {},
		strings.Repeat("d", schemaCacheMaxSourceBytes): {},
		strings.Repeat("e", schemaCacheMaxSourceBytes): {},
	}}}
	if _, eligible := compiledSchemaCacheWeight("{}", textPlan); eligible {
		t.Fatal("retained text budget was ignored")
	}
}

func schemaCacheErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func requireSchemaCacheBounds(t *testing.T, cache *compiledSchemaCache) {
	t.Helper()
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if len(cache.entries) > len(cache.order) || cache.bytes < 0 || cache.bytes > cache.maxBytes {
		t.Fatalf("cache bounds exceeded: entries=%d/%d bytes=%d/%d", len(cache.entries), len(cache.order), cache.bytes, cache.maxBytes)
	}
	sum := 0
	queued := make(map[string]bool)
	for _, key := range cache.order {
		if key == "" {
			continue
		}
		if queued[key] {
			t.Fatal("FIFO contains duplicate source")
		}
		queued[key] = true
		if _, ok := cache.entries[key]; !ok {
			t.Fatal("FIFO retained an evicted source")
		}
	}
	for key, entry := range cache.entries {
		if len(key) > schemaCacheMaxSourceBytes || entry.plan == nil || entry.weight > schemaCacheMaxPlanBytes || !queued[key] {
			t.Fatal("invalid resident entry or missing FIFO key")
		}
		sum += entry.weight
	}
	if sum != cache.bytes {
		t.Fatalf("weight accounting=%d sum=%d", cache.bytes, sum)
	}
}
