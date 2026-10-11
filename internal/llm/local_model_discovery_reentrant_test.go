package llm

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// Context hooks can consult discovery again (HTTP tracing, for example).
// Never invoke those hooks while the discovery/runtime bookkeeping is locked.
type reentrantLocalDiscoveryContext struct {
	context.Context
	base                 string
	once                 sync.Once
	errCalls, valueCalls atomic.Int32
}

func (c *reentrantLocalDiscoveryContext) Err() error {
	c.errCalls.Add(1)
	_ = ProviderRuntimeContext(c.base, "", "opaque-instance")
	return c.Context.Err()
}

func (c *reentrantLocalDiscoveryContext) Done() <-chan struct{} {
	c.once.Do(func() { InvalidateProviderModelCache(c.base) })
	return c.Context.Done()
}

func (c *reentrantLocalDiscoveryContext) Value(key any) any {
	c.valueCalls.Add(1)
	_ = ProviderRuntimeContext(c.base, "", "opaque-instance")
	return c.Context.Value(key)
}

func TestLocalNativeDiscoveryReentrantContextHooks(t *testing.T) {
	server, _, release, calls := gatedLocalDiscoveryServer(t, 200, localDiscoveryFixture)
	release()
	base := server.URL + "/v1"
	ctx := &reentrantLocalDiscoveryContext{Context: context.Background(), base: base}
	models, supported, err := fetchLocalNativeModelInfos(ctx, base, "")
	if err != nil || !supported || len(models) != 2 || calls.Load() != 1 {
		t.Fatalf("reentrant discovery: models=%+v supported=%v err=%v GETs=%d", models, supported, err, calls.Load())
	}
	if ctx.errCalls.Load() == 0 || ctx.valueCalls.Load() == 0 {
		t.Fatal("context hooks were not exercised")
	}
	if got := ProviderRuntimeContext(base, "", "opaque-instance"); got != 0 {
		t.Fatalf("context invalidation allowed stale publication: %d", got)
	}
	// Invalidating inside Done must preserve that waiter's response while
	// requiring a new GET for the next operation.
	models, supported, err = fetchLocalNativeModelInfos(context.Background(), base, "")
	if err != nil || !supported || len(models) != 2 || calls.Load() != 2 || ProviderRuntimeContext(base, "", "opaque-instance") != 8192 {
		t.Fatalf("fresh discovery after context invalidation: models=%+v supported=%v err=%v GETs=%d", models, supported, err, calls.Load())
	}
}
