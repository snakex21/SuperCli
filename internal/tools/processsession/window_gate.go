package processsession

import (
	"context"

	"supercli/internal/system/windowautomation"
)

type windowProcessGate struct {
	token chan struct{}
	users int
}

// Native background actions have no shared mouse/keyboard to arbitrate. Only
// operations on the same process identity must be serialized, including its
// modal windows and temporary screenshot restoration. Independent applications
// must not wait behind a stalled provider in another process.
func (t *Tool) lockWindowProcess(ctx context.Context, identity windowautomation.Identity) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.windowMu.Lock()
	if t.windowGates == nil {
		t.windowGates = make(map[windowautomation.Identity]*windowProcessGate)
	}
	gate := t.windowGates[identity]
	if gate == nil {
		gate = &windowProcessGate{token: make(chan struct{}, 1)}
		t.windowGates[identity] = gate
	}
	gate.users++
	t.windowMu.Unlock()
	drop := func() {
		t.windowMu.Lock()
		defer t.windowMu.Unlock()
		gate.users--
		if gate.users == 0 {
			delete(t.windowGates, identity)
		}
	}
	select {
	case gate.token <- struct{}{}:
		unlock := func() { <-gate.token; drop() }
		if err := ctx.Err(); err != nil {
			unlock()
			return nil, err
		}
		return unlock, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}
