package core

import "sync"

// RegistryDiagnosticCounts is the non-executable state needed by diagnostics.
type RegistryDiagnosticCounts struct {
	Registered int
	Visible    int
}

// RegistryDiagnostics follows registration and visibility changes without
// retaining tools, their callbacks, validators or output store. The registry
// owns this optional handle; the handle has no reference back to the registry.
type RegistryDiagnostics struct {
	mu     sync.RWMutex
	counts RegistryDiagnosticCounts
	// Only the owning Registry reads/writes revision, under Registry.mu.
	revision uint64
}

func (d *RegistryDiagnostics) Snapshot() RegistryDiagnosticCounts {
	if d == nil {
		return RegistryDiagnosticCounts{}
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.counts
}

// Diagnostics creates one small handle on demand. Keeping it after a run ends
// preserves the last counts while allowing the executable registry to be freed.
func (r *Registry) Diagnostics() *RegistryDiagnostics {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.diagnostics == nil {
		r.diagnostics = &RegistryDiagnostics{counts: r.diagnosticCountsLocked(), revision: r.revision}
	}
	return r.diagnostics
}

func (r *Registry) diagnosticCountsLocked() RegistryDiagnosticCounts {
	counts := RegistryDiagnosticCounts{Registered: len(r.tools)}
	// Match VisibleNames exactly: its registered order filters unknown always-on
	// names, and membership in both sets contributes only once.
	for _, name := range r.order {
		_, active := r.visible[name]
		_, always := r.alwaysOn[name]
		if active || always {
			counts.Visible++
		}
	}
	return counts
}

// Caller holds Registry.mu until after publication. Unobserved registries pay
// one nil check; unchanged operations do not rescan or publish a snapshot.
func (r *Registry) publishDiagnosticsLocked() {
	d := r.diagnostics
	if d == nil || d.revision == r.revision {
		return
	}
	counts := r.diagnosticCountsLocked()
	d.mu.Lock()
	d.counts = counts
	d.mu.Unlock()
	d.revision = r.revision
}
