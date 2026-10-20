package testbrowser

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryMetricsNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	b, err := Start(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	p, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Eval(ctx, `document.body.innerHTML='<button>owned fixture</button>'; document.querySelector('button').addEventListener('click',()=>{});`, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.CollectGarbage(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := p.MemoryMetrics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m.UsedSize <= 0 || m.TotalSize < m.UsedSize || m.Documents < 1 || m.Nodes < 5 || m.JSEventListeners < 1 {
		t.Fatalf("invalid counters: %+v", m)
	}
	dead, stop := context.WithCancel(ctx)
	stop()
	if err := p.CollectGarbage(dead); !errors.Is(err, context.Canceled) {
		t.Fatalf("GC cancellation: %v", err)
	}
	if _, err := p.MemoryMetrics(dead); !errors.Is(err, context.Canceled) {
		t.Fatalf("metrics cancellation: %v", err)
	}
	t.Logf("owned target heap/DOM: %+v", m)
}
