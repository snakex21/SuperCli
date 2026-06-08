package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func okTool(name string) Tool {
	return Tool{
		Name:        name,
		Description: "test " + name,
		Schema:      "{}",
		Fn: func(ctx context.Context, args json.RawMessage) (Result, error) {
			return Result{Text: "ok"}, nil
		},
	}
}

func TestTool_Validate(t *testing.T) {
	cases := []struct {
		name    string
		tool    Tool
		wantErr bool
	}{
		{"valid", okTool("x"), false},
		{"empty name", Tool{Description: "d", Fn: okTool("x").Fn}, true},
		{"empty desc", Tool{Name: "x", Fn: okTool("x").Fn}, true},
		{"nil fn", Tool{Name: "x", Description: "d"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.tool.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(okTool("x")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len = %d, want 1", r.Len())
	}
}

func TestRegistry_RejectsDuplicate(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(okTool("x"))
	if err := r.Register(okTool("x")); err == nil {
		t.Fatal("expected error on duplicate register")
	}
	if r.Len() != 1 {
		t.Fatalf("Len = %d after dup, want 1", r.Len())
	}
}

func TestRegistry_RejectsInvalid(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Tool{Name: "", Description: "x", Fn: okTool("x").Fn}); err == nil {
		t.Fatal("expected error on empty name")
	}
	if err := r.Register(Tool{Name: "y", Description: "", Fn: okTool("y").Fn}); err == nil {
		t.Fatal("expected error on empty desc")
	}
	if err := r.Register(Tool{Name: "z", Description: "z"}); err == nil {
		t.Fatal("expected error on nil Fn")
	}
}

func TestRegistry_Get(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(okTool("x"))
	got, ok := r.Get("x")
	if !ok {
		t.Fatal("Get(x) not found")
	}
	if got.Name != "x" || got.Description != "test x" {
		t.Fatalf("Get(x) = %+v", got)
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get(missing) reported found")
	}
}

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(okTool("a"))
	r.MustRegister(okTool("b"))
	r.MustRegister(okTool("c"))
	got := r.Names()
	if len(got) != 3 {
		t.Fatalf("Names = %v, want 3 entries", got)
	}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !seen[want] {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
}

func TestRegistry_EmptyLen(t *testing.T) {
	r := NewRegistry()
	if r.Len() != 0 {
		t.Fatalf("Len = %d, want 0", r.Len())
	}
	if names := r.Names(); len(names) != 0 {
		t.Fatalf("Names = %v, want empty", names)
	}
}

func TestMustRegister_PanicsOnError(t *testing.T) {
	r := NewRegistry()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	r.MustRegister(Tool{Name: ""})
}

func TestRegistry_Execute_Success(t *testing.T) {
	r := NewRegistry()
	r.MustRegister(okTool("greet"))
	res, err := r.Execute(context.Background(), "greet", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Text != "ok" {
		t.Fatalf("Text = %q, want ok", res.Text)
	}
}

func TestRegistry_Execute_Unknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.Execute(context.Background(), "nope", nil)
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("err = %v, want ErrUnknownTool", err)
	}
}
