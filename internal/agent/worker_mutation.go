package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Mutation observers are optional and scoped to delegation. Ordinary tool
// invocations retain their existing context representation.
type workerMutationKey struct{}

type workerEffectBarrierKey struct{}

func withWorkerEffectBarrier(ctx context.Context, observe func(string)) context.Context {
	if observe == nil {
		return ctx
	}
	return context.WithValue(ctx, workerEffectBarrierKey{}, observe)
}

// An attempted effect can invalidate parent evidence even on error or Stop.
// This is deliberately separate from successful-repair accounting.
func (l *Loop) workerEffectBarrierObserver(ctx context.Context) func(string) {
	root := l.baseDir
	upstream, _ := ctx.Value(workerEffectBarrierKey{}).(func(string))
	return func(childRoot string) {
		if sameWorkerWorkspace(root, childRoot) {
			l.resultReuse.reset()
			l.completedOps.reset()
		}
		if upstream != nil {
			upstream(childRoot)
		}
	}
}

func (l *Loop) forwardWorkerEffectBarrier(ctx context.Context) {
	if observe, _ := ctx.Value(workerEffectBarrierKey{}).(func(string)); observe != nil {
		observe(l.baseDir)
	}
}

func withWorkerMutationObserver(ctx context.Context, observe func(string)) context.Context {
	if observe == nil {
		return ctx
	}
	return context.WithValue(ctx, workerMutationKey{}, observe)
}

// Only verified file mutations from the currently delegated run may expire
// command failures. Expiration is linearized with the failed-check generation.
// Forward the actual workspace to ancestors only after releasing local locks.
func (l *Loop) workerMutationObserver(ctx context.Context) func(string) {
	root := l.baseDir
	upstream, _ := ctx.Value(workerMutationKey{}).(func(string))
	l.failedChecks.mu.Lock()
	generation := l.failedChecks.generation
	l.failedChecks.mu.Unlock()
	return func(childRoot string) {
		if sameWorkerWorkspace(root, childRoot) {
			l.resultReuse.reset()
			l.completedOps.reset()
			l.failedChecks.mu.Lock()
			if l.failedChecks.generation == generation {
				l.identicalFails.workspaceChanged()
			}
			l.failedChecks.mu.Unlock()
		}
		if upstream != nil {
			upstream(childRoot)
		}
	}
}

func (l *Loop) forwardWorkerMutation(ctx context.Context) {
	observe, _ := ctx.Value(workerMutationKey{}).(func(string))
	if observe != nil {
		observe(l.baseDir)
	}
}

// Exact roots need no metadata query. Aliases may share directory identity;
// a separate worktree or an unknown workspace never expires parent failures.
func sameWorkerWorkspace(parentRoot, childRoot string) bool {
	if strings.TrimSpace(parentRoot) == "" || strings.TrimSpace(childRoot) == "" {
		return false
	}
	parent, err := filepath.Abs(parentRoot)
	if err != nil {
		return false
	}
	child, err := filepath.Abs(childRoot)
	if err != nil {
		return false
	}
	if parent == child {
		return true
	}
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return false
	}
	childInfo, err := os.Stat(child)
	return err == nil && childInfo.IsDir() && os.SameFile(parentInfo, childInfo)
}
