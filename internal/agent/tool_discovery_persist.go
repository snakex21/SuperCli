package agent

import (
	"context"
	"sort"
	"strings"
)

// Tool discovery is session state, not authorization to bypass a tool's own
// validation/approval. Only names still present in the current registry restore.
type toolDiscoveryStore interface {
	ReadDiscoveredTools(context.Context) ([]string, error)
	SaveDiscoveredTools(context.Context, []string) error
}

type toolDiscoveryState struct {
	loaded bool
	saved  string
}

func discoveryKey(names []string) string {
	sort.Strings(names)
	return strings.Join(names, "\x00")
}

func (l *Loop) restoreDiscoveredTools(ctx context.Context) {
	if l.toolDiscovery.loaded || l.registry == nil {
		return
	}
	l.toolDiscovery.loaded = true
	w, ok := l.writer.(toolDiscoveryStore)
	if !ok {
		return
	}
	names, err := w.ReadDiscoveredTools(ctx)
	if err != nil {
		l.persistNotify("could not restore discovered tools: " + err.Error())
		return
	}
	l.toolDiscovery.saved = discoveryKey(names)
	l.registry.ActivateDiscovered(names...)
}

func (l *Loop) persistDiscoveredTools(ctx context.Context) {
	if !l.toolDiscovery.loaded || l.registry == nil {
		return
	}
	w, ok := l.writer.(toolDiscoveryStore)
	if !ok {
		return
	}
	names := l.registry.DiscoveredNames()
	key := discoveryKey(names)
	if key == l.toolDiscovery.saved {
		return
	}
	if err := w.SaveDiscoveredTools(ctx, names); err != nil {
		l.persistNotify("could not save discovered tools: " + err.Error())
		return
	}
	l.toolDiscovery.saved = key
}

// RestoreDiscoveredTools attaches discovery from a loaded conversation when
// /resume imports it into a loop whose writer belongs to a different session.
func (l *Loop) RestoreDiscoveredTools(names []string) {
	if l.registry == nil {
		return
	}
	l.toolDiscovery = toolDiscoveryState{loaded: true}
	l.registry.ActivateDiscovered(names...)
}
