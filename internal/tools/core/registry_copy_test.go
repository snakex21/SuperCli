package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRegisterFromPreservesValidationCoercionAndIsolation(t *testing.T) {
	var executions atomic.Int64
	source := NewRegistry()
	tool := Tool{Name: "record", Description: "fixture", ReadOnly: true, Schema: `{"type":"object","properties":{"count":{"type":"integer","minimum":1,"maximum":3},"kind":{"enum":["fact","decision"]},"tag":{"type":"string","pattern":"^[a-z]+$"}},"required":["count","kind","tag"]}`, Fn: func(_ context.Context, args json.RawMessage) (Result, error) {
		executions.Add(1)
		return Result{Text: string(args)}, nil
	}}
	source.MustRegister(tool)
	source.MarkAlwaysOn(tool.Name)
	child := NewRegistry()
	if err := child.RegisterFrom(source, "record"); err != nil {
		t.Fatal(err)
	}
	if child.IsVisible("record") || child.IsActive("record") {
		t.Fatal("visibility inherited")
	}
	child.ActivateDiscovered("record")
	if source.IsActive("record") {
		t.Fatal("child activation changed source")
	}
	// Changing the caller's old Tool value does not alter either registered copy.
	tool.Schema = "false"
	tool.Fn = func(context.Context, json.RawMessage) (Result, error) {
		t.Error("used unregistered callback")
		return Result{}, nil
	}
	for _, reg := range []*Registry{source, child} {
		valid := json.RawMessage(`{"count":"2","kind":"fact","tag":"abc"}`)
		result, err := reg.Execute(context.Background(), "record", valid)
		if err != nil || result.Err != nil || !strings.Contains(result.Text, `"count":2`) {
			t.Fatalf("coercion changed: %v %v %s", err, result.Err, result.Text)
		}
		for _, invalid := range []string{
			`{"count":0,"kind":"fact","tag":"abc"}`,
			`{"count":4,"kind":"fact","tag":"abc"}`,
			`{"count":2,"kind":"project","tag":"abc"}`,
			`{"count":2,"kind":"fact","tag":"A1"}`,
			`{"count":2,"kind":"fact","tag":"abc","unknown":true}`,
		} {
			before := executions.Load()
			result, err = reg.Execute(context.Background(), "record", json.RawMessage(invalid))
			if err == nil && result.Err == nil {
				t.Errorf("accepted invalid arguments: %s", invalid)
			}
			if executions.Load() != before {
				t.Fatal("invalid call reached implementation")
			}
		}
	}
	if executions.Load() != 2 {
		t.Fatalf("valid calls=%d", executions.Load())
	}
}

func TestRegisterFromErrorsLeaveTargetUnchanged(t *testing.T) {
	source := NewRegistry()
	source.MustRegister(Tool{Name: "existing", Description: "source", Schema: "false", Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{Text: "wrong"}, nil }})
	target := NewRegistry()
	target.MustRegister(Tool{Name: "existing", Description: "target", Schema: "", Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{Text: "unchanged"}, nil }})
	if !errors.Is(target.RegisterFrom(source, "missing"), ErrUnknownTool) {
		t.Fatal("missing source not reported")
	}
	if target.RegisterFrom(nil, "missing") == nil {
		t.Fatal("nil source not reported")
	}
	if target.RegisterFrom(source, "existing") == nil || target.RegisterFrom(target, "existing") == nil {
		t.Fatal("duplicate accepted")
	}
	if target.Len() != 1 {
		t.Fatal("failed copy mutated target")
	}
	result, err := target.Execute(context.Background(), "existing", json.RawMessage(`{"arbitrary":true}`))
	if err != nil || result.Err != nil || result.Text != "unchanged" {
		t.Fatal("duplicate overwrote existing tool or validator")
	}
}

func TestRegisterFromAllowsEmptyAndBooleanSchemas(t *testing.T) {
	for _, schema := range []string{"", "true", "false", "{}"} {
		source, target := NewRegistry(), NewRegistry()
		source.MustRegister(Tool{Name: "fixture", Description: "fixture", Schema: schema, Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{Text: "ok"}, nil }})
		if err := target.RegisterFrom(source, "fixture"); err != nil {
			t.Fatal(err)
		}
		result, err := target.Execute(context.Background(), "fixture", json.RawMessage("{}"))
		failed := err != nil || result.Err != nil
		if failed != (schema == "false") {
			t.Fatalf("schema %q changed behavior: %v %v", schema, err, result.Err)
		}
	}
}

func TestRegisterFromConcurrentChildValidation(t *testing.T) {
	source := NewRegistry()
	source.MustRegister(Tool{Name: "bounded", Description: "fixture", Schema: `{"type":"object","properties":{"value":{"type":"number","minimum":0.1,"maximum":5.2}},"required":["value"]}`, Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{Text: "ok"}, nil }})
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			child := NewRegistry()
			if err := child.RegisterFrom(source, "bounded"); err != nil {
				t.Error(err)
				return
			}
			for range 20 {
				result, err := child.Execute(context.Background(), "bounded", json.RawMessage(`{"value":"1.5"}`))
				if err != nil || result.Err != nil {
					t.Errorf("valid call rejected: %v %v", err, result.Err)
					return
				}
				result, err = child.Execute(context.Background(), "bounded", json.RawMessage(`{"value":10}`))
				if err == nil && result.Err == nil {
					t.Error("invalid call accepted")
					return
				}
			}
		}()
	}
	wg.Wait()
}
