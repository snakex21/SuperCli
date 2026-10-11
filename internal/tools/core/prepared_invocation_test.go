package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"supercli/internal/tools/core"
)

func preparedInvocationFixture(t *testing.T) (*core.Registry, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	reg := core.NewRegistry()
	repairs, executions := new(atomic.Int64), new(atomic.Int64)
	reg.MustRegister(core.Tool{
		Name: "fixture", Description: "prepared argument fixture",
		Schema: `{"type":"object","properties":{"count":{"type":"integer","minimum":1,"maximum":3},"enabled":{"type":"boolean"},"paths":{"type":"array","items":{"type":"string"}},"offset":{"type":"integer"}},"required":["count"],"additionalProperties":false}`,
		RepairArgs: func(raw json.RawMessage) (json.RawMessage, bool) {
			repairs.Add(1)
			switch string(raw) {
			case `{"legacy":2}`:
				return json.RawMessage(`{"count":"2"}`), true
			case `{"legacy":4}`:
				return json.RawMessage(`{"count":"4"}`), true
			default:
				return nil, false
			}
		},
		Fn: func(_ context.Context, args json.RawMessage) (core.Result, error) {
			executions.Add(1)
			return core.Result{Text: string(args)}, nil
		},
	})
	return reg, repairs, executions
}

func TestPreparedInvocationMatchesExecutionPreparation(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		invalid   bool
	}{
		{"valid", `{"count":1}`, false},
		{"scalar and encoded array coercion", `{"count":"2","enabled":"true","paths":"[\"a\",\"b\"]","offset":9007199254740993}`, false},
		{"pure repair", `{"legacy":2}`, false},
		{"minimum", `{"count":0}`, true},
		{"additional property", `{"count":1,"extra":true}`, true},
		{"malformed", `{`, true},
		{"repaired candidate still invalid", `{"legacy":4}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, repairs, executions := preparedInvocationFixture(t)
			// Ordinary Execute is the existing byte/error oracle, not a second
			// implementation of coercion, repair or schema validation.
			expected, expectedErr := reg.Execute(context.Background(), "fixture", json.RawMessage(tc.raw))
			repairs.Store(0)
			executions.Store(0)
			invocation, accepted, err := reg.PrepareInvocation("fixture", json.RawMessage(tc.raw))
			if executions.Load() != 0 {
				t.Fatal("preparation executed the tool")
			}
			if tc.invalid {
				if expectedErr != nil || !errors.Is(expected.Err, core.ErrInvalidToolArgs) || !errors.Is(err, core.ErrInvalidToolArgs) || err.Error() != expected.Err.Error() || accepted != nil {
					t.Fatalf("schema error classification changed: ordinary=%v/%v prepared=%v", expectedErr, expected.Err, err)
				}
				if result, executeErr := reg.ExecutePrepared(context.Background(), invocation); executeErr == nil || result.Err == nil || executions.Load() != 0 {
					t.Fatal("failed preparation produced an executable token")
				}
				return
			}
			if expectedErr != nil || expected.Err != nil || err != nil || string(accepted) != expected.Text {
				t.Fatalf("accepted bytes differ: ordinary=%q/%v/%v prepared=%q/%v", expected.Text, expectedErr, expected.Err, accepted, err)
			}
			result, executeErr := reg.ExecutePrepared(context.Background(), invocation)
			if executeErr != nil || result.Err != nil || result.Text != expected.Text || executions.Load() != 1 {
				t.Fatalf("execution differs: result=%+v err=%v calls=%d", result, executeErr, executions.Load())
			}
			wantRepairs := int64(0)
			if tc.name == "pure repair" {
				wantRepairs = 1
			}
			if repairs.Load() != wantRepairs {
				t.Fatalf("pure repair repeated: got=%d want=%d", repairs.Load(), wantRepairs)
			}
		})
	}
}

func TestPreparedInvocationRejectsZeroForeignAndUnknown(t *testing.T) {
	owner, _, executions := preparedInvocationFixture(t)
	foreign := core.NewRegistry()
	if err := foreign.RegisterFrom(owner, "fixture"); err != nil {
		t.Fatal(err)
	}
	invocation, _, err := owner.PrepareInvocation("fixture", json.RawMessage(`{"count":1}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		registry   *core.Registry
		invocation core.PreparedInvocation
	}{
		{"zero", owner, core.PreparedInvocation{}},
		{"foreign identical contract", foreign, invocation},
		{"nil receiver", nil, invocation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.registry.ExecutePrepared(context.Background(), tc.invocation)
			if err == nil || result.Err == nil || executions.Load() != 0 {
				t.Fatalf("invalid token reached tool: result=%+v err=%v calls=%d", result, err, executions.Load())
			}
		})
	}
	zero, accepted, err := owner.PrepareInvocation("missing", json.RawMessage(`{}`))
	if !errors.Is(err, core.ErrUnknownTool) || accepted != nil {
		t.Fatalf("unknown preparation changed classification: %v", err)
	}
	if _, err := owner.ExecutePrepared(context.Background(), zero); err == nil || executions.Load() != 0 {
		t.Fatal("unknown preparation produced an executable token")
	}
}

func TestPreparedInvocationOwnsArgumentsAcrossCallerAndCallbackMutation(t *testing.T) {
	reg := core.NewRegistry()
	var executions atomic.Int64
	reg.MustRegister(core.Tool{
		Name: "bounded", Description: "mutable callback fixture",
		Schema: `{"type":"object","properties":{"count":{"type":"integer","maximum":1}},"required":["count"]}`,
		Fn: func(_ context.Context, args json.RawMessage) (core.Result, error) {
			executions.Add(1)
			before := string(args)
			// Invalid outside the accepted schema. This must affect only the
			// callback's execution buffer, never the opaque accepted token.
			copy(args, `{"count":9}`)
			return core.Result{Text: before}, nil
		},
	})
	raw := json.RawMessage(`{"count":1}`)
	invocation, accepted, err := reg.PrepareInvocation("bounded", raw)
	if err != nil {
		t.Fatal(err)
	}
	copy(raw, `{"count":7}`)
	copy(accepted, `{"count":8}`)
	rawAfterMutation, acceptedAfterMutation := string(raw), string(accepted)
	for range 2 {
		result, err := reg.ExecutePrepared(context.Background(), invocation)
		if err != nil || result.Err != nil || result.Text != `{"count":1}` {
			t.Fatalf("accepted arguments were mutated: result=%+v err=%v", result, err)
		}
	}
	if executions.Load() != 2 || string(raw) != rawAfterMutation || string(accepted) != acceptedAfterMutation {
		t.Fatal("execution shared caller/replay buffers")
	}
}

func TestPreparedInvocationPreservesNilEmptyAndFailOpenArguments(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   json.RawMessage
		schema string
	}{
		{"nil without schema", nil, ""},
		{"empty without schema", json.RawMessage{}, ""},
		{"arbitrary without schema", json.RawMessage("opaque input"), ""},
		{"valid without schema", json.RawMessage(`{}`), ""},
		{"unsupported keyword", json.RawMessage(`{}`), `{"type":"object","unevaluatedProperties":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := core.NewRegistry()
			var seenNil bool
			reg.MustRegister(core.Tool{Name: "permissive", Description: "existing fail-open contract", Schema: tc.schema, Fn: func(_ context.Context, args json.RawMessage) (core.Result, error) {
				seenNil = args == nil
				return core.Result{Text: string(args)}, nil
			}})
			expected, expectedErr := reg.Execute(context.Background(), "permissive", tc.args)
			expectedNil := seenNil
			invocation, accepted, prepareErr := reg.PrepareInvocation("permissive", tc.args)
			if expectedErr != nil || expected.Err != nil || prepareErr != nil || (accepted == nil) != expectedNil {
				t.Fatalf("permissive preparation changed: ordinary=%v/%v prepare=%v nil=%t/%t", expectedErr, expected.Err, prepareErr, accepted == nil, expectedNil)
			}
			result, err := reg.ExecutePrepared(context.Background(), invocation)
			if err != nil || result.Err != nil || result.Text != expected.Text || seenNil != expectedNil {
				t.Fatalf("nil/empty execution changed: %+v %v nil=%t/%t", result, err, seenNil, expectedNil)
			}
		})
	}
}

func TestPreparedInvocationRechecksCurrentExecutionPermissionAndContext(t *testing.T) {
	type permissionKey struct{}
	type markerKey struct{}
	permissionDenied := errors.New("permission denied now")
	transportFailed := errors.New("execution failed")
	reg := core.NewRegistry()
	var calls atomic.Int64
	reg.MustRegister(core.Tool{
		Name: "guarded", Description: "live permission fixture", Schema: `{"type":"object"}`,
		Fn: func(ctx context.Context, _ json.RawMessage) (core.Result, error) {
			calls.Add(1)
			if err := ctx.Err(); err != nil {
				return core.Result{Text: "partial receipt", Err: err}, nil
			}
			if ctx.Value(permissionKey{}) != true {
				return core.Result{Err: permissionDenied}, nil
			}
			if ctx.Value(markerKey{}) == "fail" {
				return core.Result{Text: "failed receipt"}, transportFailed
			}
			return core.Result{Text: fmt.Sprint(ctx.Value(markerKey{}))}, nil
		},
	})
	invocation, _, err := reg.PrepareInvocation("guarded", json.RawMessage(`{}`))
	if err != nil || calls.Load() != 0 {
		t.Fatal("preparation performed a permission check")
	}
	allowed := context.WithValue(context.WithValue(context.Background(), permissionKey{}, true), markerKey{}, "current invocation")
	if result, err := reg.ExecutePrepared(allowed, invocation); err != nil || result.Err != nil || result.Text != "current invocation" {
		t.Fatalf("current context was not preserved: %+v %v", result, err)
	}
	if result, err := reg.ExecutePrepared(context.Background(), invocation); err != nil || !errors.Is(result.Err, permissionDenied) {
		t.Fatalf("prepared call bypassed revoked permission: %+v %v", result, err)
	}
	failed := context.WithValue(allowed, markerKey{}, "fail")
	if result, err := reg.ExecutePrepared(failed, invocation); !errors.Is(err, transportFailed) || result.Text != "failed receipt" {
		t.Fatalf("callback failure changed: %+v %v", result, err)
	}
	canceled, cancel := context.WithCancel(allowed)
	cancel()
	if result, err := reg.ExecutePrepared(canceled, invocation); err != nil || !errors.Is(result.Err, context.Canceled) || result.Text != "partial receipt" {
		t.Fatalf("callback cancellation/partial result changed: %+v %v", result, err)
	}
	if calls.Load() != 4 {
		t.Fatalf("live callback checks=%d want=4", calls.Load())
	}
}

func TestPreparedInvocationSurvivesUnrelatedRegistrationAndVisibility(t *testing.T) {
	reg, _, executions := preparedInvocationFixture(t)
	invocation, _, err := reg.PrepareInvocation("fixture", json.RawMessage(`{"count":1}`))
	if err != nil {
		t.Fatal(err)
	}
	reg.MustRegister(core.Tool{Name: "different", Description: "independent contract", Schema: "false", Fn: func(context.Context, json.RawMessage) (core.Result, error) {
		t.Fatal("executed a different registered contract")
		return core.Result{}, nil
	}})
	reg.ActivateDiscovered("fixture", "different")
	reg.ResetVisibility()
	if err := reg.Register(core.Tool{Name: "fixture", Description: "duplicate must not replace", Schema: "false", Fn: func(context.Context, json.RawMessage) (core.Result, error) {
		t.Fatal("duplicate contract replaced the accepted tool")
		return core.Result{}, nil
	}}); err == nil {
		t.Fatal("duplicate registration accepted")
	}
	result, err := reg.ExecutePrepared(context.Background(), invocation)
	if err != nil || result.Err != nil || result.Text != `{"count":1}` || executions.Load() != 1 {
		t.Fatalf("unrelated changes invalidated append-only contract: %+v %v", result, err)
	}
	if reg.IsVisible("fixture") || reg.IsVisible("different") {
		t.Fatal("prepared execution changed visibility")
	}
}

func TestPreparedInvocationConcurrentIndependentCalls(t *testing.T) {
	reg := core.NewRegistry()
	var calls atomic.Int64
	reg.MustRegister(core.Tool{
		Name: "independent", Description: "independent concurrent invocation", Schema: `{"type":"object","properties":{"count":{"type":"integer","minimum":0}},"required":["count"]}`,
		Fn: func(_ context.Context, args json.RawMessage) (core.Result, error) {
			calls.Add(1)
			before := string(args)
			for i := range args {
				args[i] = '!'
			}
			return core.Result{Text: before}, nil
		},
	})
	const workers = 16
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw := json.RawMessage(fmt.Sprintf(`{"count":%d}`, i))
			expected := string(raw)
			invocation, accepted, err := reg.PrepareInvocation("independent", raw)
			if err != nil {
				t.Error(err)
				return
			}
			for j := range raw {
				raw[j] = '?'
				accepted[j] = '#'
			}
			var sameToken sync.WaitGroup
			for range 2 {
				sameToken.Add(1)
				go func() {
					defer sameToken.Done()
					result, err := reg.ExecutePrepared(context.Background(), invocation)
					if err != nil || result.Err != nil || result.Text != expected {
						t.Errorf("independent call changed: got=%q want=%q err=%v/%v", result.Text, expected, err, result.Err)
					}
				}()
			}
			sameToken.Wait()
		}()
	}
	wg.Wait()
	if calls.Load() != 2*workers {
		t.Fatalf("independent executions=%d want=%d", calls.Load(), 2*workers)
	}
}
