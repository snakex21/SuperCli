package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func schemaResidencyUncached(t *testing.T, raw string, mode toolSchemaMode) json.RawMessage {
	t.Helper()
	value, err := normalizedToolSchemaObjectChecked(raw)
	if err != nil {
		t.Fatal(err)
	}
	if mode == toolSchemaPortable {
		rewritePortableToolSchema(value, true)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func assertSchemaResidencyAccounting(t *testing.T) {
	t.Helper()
	normalizedToolSchemaCache.Lock()
	defer normalizedToolSchemaCache.Unlock()
	total := 0
	for key, value := range normalizedToolSchemaCache.entries {
		total += len(key.raw) + len(value)
	}
	if total != normalizedToolSchemaCache.bytes {
		t.Fatalf("accounted bytes=%d, actual payload=%d", normalizedToolSchemaCache.bytes, total)
	}
	if total > toolSchemaCacheByteLimit {
		t.Fatalf("cache payload=%d exceeds budget=%d", total, toolSchemaCacheByteLimit)
	}
	if len(normalizedToolSchemaCache.entries) > toolSchemaCacheLimit {
		t.Fatalf("entry count=%d exceeds limit=%d", len(normalizedToolSchemaCache.entries), toolSchemaCacheLimit)
	}
	if len(normalizedToolSchemaCache.order) != len(normalizedToolSchemaCache.entries) {
		t.Fatal("order and entries differ")
	}
	seen := make(map[toolSchemaCacheKey]bool)
	for _, key := range normalizedToolSchemaCache.order {
		if seen[key] || normalizedToolSchemaCache.entries[key] == nil {
			t.Fatal("duplicate or missing cache order key")
		}
		seen[key] = true
	}
	backing := normalizedToolSchemaCache.order[:cap(normalizedToolSchemaCache.order)]
	for _, key := range backing[len(normalizedToolSchemaCache.order):] {
		if key.raw != "" {
			t.Fatal("eviction retains raw key beyond order length")
		}
	}
}

func TestSchemaResidencyByteBudgetPreservesBothForms(t *testing.T) {
	resetSchemaResidencyCache()
	t.Cleanup(resetSchemaResidencyCache)
	const count = 24 // Both forms exceed the byte budget before the count limit.
	for i := 0; i < count; i++ {
		raw := schemaResidencyLarge(i, 256<<10)
		for _, mode := range []toolSchemaMode{toolSchemaFull, toolSchemaPortable} {
			got, err := cachedToolSchema(raw, mode)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, schemaResidencyUncached(t, raw, mode)) {
				t.Fatalf("mode=%d schema=%d changed after caching", mode, i)
			}
		}
		assertSchemaResidencyAccounting(t)
	}
	entries, _ := schemaResidencyPayload()
	if entries >= 2*count {
		t.Fatal("byte pressure did not evict any entries")
	}
	firstKey := toolSchemaCacheKey{mode: toolSchemaFull, raw: schemaResidencyLarge(0, 256<<10)}
	lastKey := toolSchemaCacheKey{mode: toolSchemaPortable, raw: schemaResidencyLarge(count-1, 256<<10)}
	normalizedToolSchemaCache.Lock()
	_, firstPresent := normalizedToolSchemaCache.entries[firstKey]
	_, lastPresent := normalizedToolSchemaCache.entries[lastKey]
	normalizedToolSchemaCache.Unlock()
	if firstPresent || !lastPresent {
		t.Fatal("FIFO byte eviction did not keep the newest schema")
	}
	// A removed form is still compiled exactly; eviction is residency only.
	got, err := cachedToolSchema(firstKey.raw, firstKey.mode)
	if err != nil || !bytes.Equal(got, schemaResidencyUncached(t, firstKey.raw, firstKey.mode)) {
		t.Fatalf("evicted schema changed when recompiled: %v", err)
	}
	assertSchemaResidencyAccounting(t)
}

func TestSchemaResidencyCountBudgetAccountsBytes(t *testing.T) {
	resetSchemaResidencyCache()
	t.Cleanup(resetSchemaResidencyCache)
	for i := 0; i < toolSchemaCacheLimit+40; i++ {
		if _, err := cachedToolSchema(schemaResidencyLarge(i, i%101), toolSchemaMode(i%2)); err != nil {
			t.Fatal(err)
		}
	}
	assertSchemaResidencyAccounting(t)
	entries, _ := schemaResidencyPayload()
	if entries != toolSchemaCacheLimit {
		t.Fatalf("entries=%d, want=%d", entries, toolSchemaCacheLimit)
	}
}

func TestSchemaResidencyOversizeIsNotRejectedOrCached(t *testing.T) {
	resetSchemaResidencyCache()
	t.Cleanup(resetSchemaResidencyCache)
	if _, err := CompileToolSchema(schemaResidencySmall); err != nil {
		t.Fatal(err)
	}
	entriesBefore, payloadBefore := schemaResidencyPayload()
	// raw fits on its own; raw + normalized value exceeds the residency budget.
	raw := schemaResidencyLarge(1, toolSchemaCacheByteLimit/2)
	if len(raw) >= toolSchemaCacheByteLimit {
		t.Fatal("fixture does not exercise combined key/value size")
	}
	for _, mode := range []toolSchemaMode{toolSchemaFull, toolSchemaPortable} {
		want := schemaResidencyUncached(t, raw, mode)
		got, err := cachedToolSchema(raw, mode)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("oversized valid schema changed: mode=%d err=%v", mode, err)
		}
		got[0] = '['
		again, err := cachedToolSchema(raw, mode)
		if err != nil || !bytes.Equal(again, want) {
			t.Fatalf("oversized result shares caller-owned bytes: mode=%d err=%v", mode, err)
		}
	}
	entriesAfter, payloadAfter := schemaResidencyPayload()
	if entriesAfter != entriesBefore || payloadAfter != payloadBefore {
		t.Fatal("oversized schema evicted or entered the cache")
	}
	assertSchemaResidencyAccounting(t)
}

func TestSchemaResidencyKeysOwnOnlyTrimmedInput(t *testing.T) {
	resetSchemaResidencyCache()
	t.Cleanup(resetSchemaResidencyCache)
	input := strings.Repeat(" ", 1<<22) + schemaResidencySmall + strings.Repeat("\n", 1<<22)
	trimmed := strings.TrimSpace(input)
	if _, err := cachedToolSchema(input, toolSchemaPortable); err != nil {
		t.Fatal(err)
	}
	normalizedToolSchemaCache.Lock()
	for key := range normalizedToolSchemaCache.entries {
		if key.raw != trimmed {
			normalizedToolSchemaCache.Unlock()
			t.Fatal("cached key differs from normalized input")
		}
		if unsafe.StringData(key.raw) == unsafe.StringData(trimmed) {
			normalizedToolSchemaCache.Unlock()
			t.Fatal("cached key retains the larger original backing string")
		}
	}
	normalizedToolSchemaCache.Unlock()
	assertSchemaResidencyAccounting(t)
}

func TestSchemaResidencyConcurrentMissesAndOutputIsolation(t *testing.T) {
	resetSchemaResidencyCache()
	t.Cleanup(resetSchemaResidencyCache)
	raw := schemaResidencyLarge(0, 32<<10)
	want := [2]json.RawMessage{
		schemaResidencyUncached(t, raw, toolSchemaFull),
		schemaResidencyUncached(t, raw, toolSchemaPortable),
	}
	const parallel = 32
	start := make(chan struct{})
	errors := make(chan error, parallel)
	var workers sync.WaitGroup
	for i := 0; i < parallel; i++ {
		mode := toolSchemaMode(i % 2)
		workers.Go(func() {
			<-start
			got, err := cachedToolSchema(raw, mode)
			if err != nil {
				errors <- err
				return
			}
			if !bytes.Equal(got, want[mode]) {
				errors <- fmt.Errorf("incorrect concurrent result for mode=%d", mode)
				return
			}
			got[0] = '['
		})
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	entries, _ := schemaResidencyPayload()
	if entries != 2 {
		t.Fatalf("concurrent same-key misses made %d entries", entries)
	}
	for _, mode := range []toolSchemaMode{toolSchemaFull, toolSchemaPortable} {
		again, err := cachedToolSchema(raw, mode)
		if err != nil || !bytes.Equal(again, want[mode]) {
			t.Fatalf("caller mutation leaked into cached mode=%d: %v", mode, err)
		}
	}
	assertSchemaResidencyAccounting(t)

	// Different-key misses also share the budget correctly under byte pressure.
	var concurrentErrors sync.Map
	for i := 0; i < parallel; i++ {
		workers.Go(func() {
			mode := toolSchemaMode(i % 2)
			if _, err := cachedToolSchema(schemaResidencyLarge(i+1, 256<<10), mode); err != nil {
				concurrentErrors.Store(i, err)
			}
		})
	}
	workers.Wait()
	concurrentErrors.Range(func(_, value any) bool {
		t.Errorf("different-key miss: %v", value)
		return true
	})
	assertSchemaResidencyAccounting(t)
}

func TestSchemaResidencyFullPortableStayIndependent(t *testing.T) {
	resetSchemaResidencyCache()
	t.Cleanup(resetSchemaResidencyCache)
	raw := `{"type":"object","properties":{"choice":{"type":"object","anyOf":[{"required":["left"]},{"required":["right"]}]}}}`
	compiled, err := CompileToolSchema(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(compiled.Full, compiled.Portable) {
		t.Fatal("fixture does not distinguish complete and portable forms")
	}
	wantFull := schemaResidencyUncached(t, raw, toolSchemaFull)
	wantPortable := schemaResidencyUncached(t, raw, toolSchemaPortable)
	compiled.Full[0], compiled.Portable[0] = '[', '['
	again, err := CompileToolSchema(raw)
	if err != nil || !bytes.Equal(again.Full, wantFull) || !bytes.Equal(again.Portable, wantPortable) {
		t.Fatalf("one caller changed cached wire forms: %v", err)
	}
	assertSchemaResidencyAccounting(t)
}

func TestSchemaResidencyExactByteBoundaryAndBulkEviction(t *testing.T) {
	resetSchemaResidencyCache()
	t.Cleanup(resetSchemaResidencyCache)
	for i := 0; i < 64; i++ {
		if _, err := cachedToolSchema(schemaResidencyLarge(i, 0), toolSchemaFull); err != nil {
			t.Fatal(err)
		}
	}
	// This complete schema normalizes without changing its length, making the
	// combined owned key/value payload exactly the budget.
	raw := schemaResidencyLarge(0, toolSchemaCacheByteLimit/2-len(schemaResidencyLarge(0, 0)))
	want := schemaResidencyUncached(t, raw, toolSchemaFull)
	if len(raw)+len(want) != toolSchemaCacheByteLimit {
		t.Fatal("fixture is not exactly at the byte boundary")
	}
	got, err := cachedToolSchema(raw, toolSchemaFull)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("boundary schema was changed: %v", err)
	}
	entries, payload := schemaResidencyPayload()
	if entries != 1 || payload != toolSchemaCacheByteLimit {
		t.Fatalf("boundary schema residency: entries=%d payload=%d", entries, payload)
	}
	assertSchemaResidencyAccounting(t)
	// A smaller next entry must remove the large one and release every old order
	// slot; it must not be blocked by stale byte accounting.
	if _, err := cachedToolSchema(schemaResidencySmall, toolSchemaFull); err != nil {
		t.Fatal(err)
	}
	assertSchemaResidencyAccounting(t)
}
