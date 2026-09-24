package consult

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"supercli/internal/llm"
)

func TestConsultCanceledDuringPoolLoadMakesNoInference(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int32
	c := &Council{
		Judge: &judgeStub{},
		LoadSamples: func() []llm.Provider {
			cancel()
			return []llm.Provider{&stubProvider{name: "sample", calls: &calls}}
		},
	}
	if _, err := c.Consult(ctx, Request{Question: "q"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("canceled consultation started inference")
	}
}
