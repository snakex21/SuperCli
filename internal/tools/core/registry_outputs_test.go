package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestRegistrySharingOutputsKeepsToolsAndSessionsSeparate(t *testing.T) {
	parent := NewRegistry()
	parent.MustRegister(Tool{Name: "parent_only", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{Text: "parent"}, nil }})
	parent.MarkAlwaysOn("parent_only")
	parent.EnsureReadOutput()
	child := NewRegistrySharingOutputs(parent)
	if len(child.Names()) != 0 {
		t.Fatal("child copied tool permissions")
	}
	child.EnsureReadOutput()
	child.MustRegister(Tool{Name: "child_only", Description: "fixture", Schema: "{}", Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{Text: "child"}, nil }})
	child.ActivateDiscovered("child_only")
	if _, ok := parent.Get("child_only"); ok {
		t.Fatal("child registered a parent tool")
	}
	if child.IsVisible("parent_only") || parent.IsVisible("child_only") {
		t.Fatal("visibility shared")
	}
	readers := []*Registry{parent, child, NewRegistrySharingOutputs(parent)}
	for _, writer := range []*Registry{parent, child} {
		body := strings.Repeat("full evidence\n", 1000)
		preview := writer.CompactModelOutput("fixture_log", body)
		handle := StoredOutputHandle(preview)
		args, _ := json.Marshal(map[string]any{"handle": handle, "query": "full evidence"})
		for _, reader := range readers {
			reader.EnsureReadOutput()
			result, err := reader.Execute(context.Background(), "read_output", args)
			if err != nil || result.Err != nil || !strings.Contains(result.Text, "full evidence") {
				t.Fatalf("family reference unavailable: %v %v", err, result.Err)
			}
		}
		unrelated := NewRegistry()
		unrelated.EnsureReadOutput()
		result, err := unrelated.Execute(context.Background(), "read_output", args)
		if err == nil && result.Err == nil {
			t.Fatal("reference leaked into an unrelated registry")
		}
	}
}

func TestSharedOutputAvoidsDuplicatePersistenceRead(t *testing.T) {
	backend := &memoryOutputPersistence{}
	ctx := WithOutputPersistence(context.Background(), backend)
	parent := NewRegistry()
	child := NewRegistrySharingOutputs(parent)
	childBody := strings.Repeat("recorded evidence\n", 1000)
	preview := child.ModelResultContentContext(ctx, "fixture_log", Result{Text: childBody})
	handle := StoredOutputHandle(preview)
	parent.EnsureReadOutput()
	args, _ := json.Marshal(map[string]any{"handle": handle, "query": "recorded evidence"})
	result, err := parent.Execute(ctx, "read_output", args)
	if err != nil || result.Err != nil || backend.saves != 1 || backend.reads != 0 {
		t.Fatalf("shared read: saves=%d reads=%d err=%v %v", backend.saves, backend.reads, err, result.Err)
	}
	// A fresh unrelated registry still restores from the existing persistence.
	restored := NewRegistry()
	restored.EnsureReadOutput()
	result, err = restored.Execute(ctx, "read_output", args)
	if err != nil || result.Err != nil || backend.reads != 1 {
		t.Fatal("durable restart recovery changed")
	}
}

func TestSharedOutputStoreConcurrentWorkersAndGlobalLimit(t *testing.T) {
	parent := NewRegistry()
	parent.EnsureReadOutput()
	const workers = 8
	children := make([]*Registry, workers)
	for i := range children {
		children[i] = NewRegistrySharingOutputs(parent)
		children[i].EnsureReadOutput()
	}
	var wg sync.WaitGroup
	for i, child := range children {
		wg.Add(1)
		go func() {
			defer wg.Done()
			marker := fmt.Sprintf("worker-%d-evidence", i)
			preview := child.CompactModelOutput("fixture_log", strings.Repeat(marker+"\n", 800))
			args, _ := json.Marshal(map[string]any{"handle": StoredOutputHandle(preview), "query": marker})
			result, err := parent.Execute(context.Background(), "read_output", args)
			if err != nil || result.Err != nil || !strings.Contains(result.Text, marker) {
				t.Errorf("concurrent evidence unavailable: %v %v", err, result.Err)
			}
		}()
	}
	wg.Wait()
	if len(parent.outputs.entries) != workers {
		t.Fatal("outputs duplicated or missing")
	}
	for i := 0; i < outputStoreItems+1; i++ {
		children[i%workers].CompactModelOutput("fixture_log", strings.Repeat("bounded output\n", 1000))
	}
	if len(parent.outputs.entries) != outputStoreItems || parent.outputs.bytes > outputStoreBytes {
		t.Fatalf("shared limit exceeded: items=%d bytes=%d", len(parent.outputs.entries), parent.outputs.bytes)
	}
}
