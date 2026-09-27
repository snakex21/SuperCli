package core

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReadOnlyCallKeyUsesExecutionCoercion(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(Tool{Name: "read", Description: "fixture", ReadOnly: true,
		Schema: "{\"type\":\"object\",\"properties\":{\"offset\":{\"type\":\"integer\"},\"label\":{\"type\":\"string\"},\"paths\":{\"type\":\"array\",\"items\":{\"type\":\"string\"}}},\"required\":[\"offset\"],\"additionalProperties\":false}",
		Fn:     func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil },
	})
	key, ok := reg.ReadOnlyCallKey("read", json.RawMessage("{\"offset\":3072,\"label\":\"7\",\"paths\":[\"a\",\"b\"]}"))
	equivalent, sameOK := reg.ReadOnlyCallKey("read", json.RawMessage("{\"paths\":\"[\\\"a\\\",\\\"b\\\"]\",\"label\":\"7\",\"offset\":\"3072\"}"))
	if !ok || !sameOK || key != equivalent {
		t.Fatal("execution-equivalent argument forms differ")
	}
	for _, args := range []string{
		"{\"offset\":3073,\"label\":\"7\",\"paths\":[\"a\",\"b\"]}",
		"{\"offset\":3072,\"label\":\"8\",\"paths\":[\"a\",\"b\"]}",
	} {
		other, ok := reg.ReadOnlyCallKey("read", json.RawMessage(args))
		if !ok || other == key {
			t.Fatalf("different read collapsed: %s", args)
		}
	}
	large, ok := reg.ReadOnlyCallKey("read", json.RawMessage("{\"offset\":9007199254740992}"))
	next, nextOK := reg.ReadOnlyCallKey("read", json.RawMessage("{\"offset\":9007199254740993}"))
	if !ok || !nextOK || large == next {
		t.Fatal("large offsets lost integer precision")
	}
	for _, args := range []string{"null", "{}", "{\"offset\":\"bad\"}", "{\"offset\":1,\"extra\":true}", "{\"offset\":1} trailing"} {
		if _, ok := reg.ReadOnlyCallKey("read", json.RawMessage(args)); ok {
			t.Fatalf("invalid call eligible: %s", args)
		}
	}
	reg.MustRegister(Tool{Name: "write", Description: "mutation", Fn: func(context.Context, json.RawMessage) (Result, error) { return Result{}, nil }})
	for _, name := range []string{"write", "unknown"} {
		if _, ok := reg.ReadOnlyCallKey(name, json.RawMessage("{}")); ok {
			t.Fatalf("%s eligible for coalescing", name)
		}
	}
}
