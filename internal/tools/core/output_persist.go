package core

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// OutputPersistence stores immutable results outside the prompt. Embedders may
// attach a portable store to one invocation; no registry-global session binding
// is mutated when parent and worker tool calls run concurrently.
type OutputPersistence interface {
	SaveToolOutput(context.Context, string, string) error
	ReadToolOutput(context.Context, string) (string, error)
}

type outputPersistenceKey struct{}

func WithOutputPersistence(ctx context.Context, store OutputPersistence) context.Context {
	return context.WithValue(ctx, outputPersistenceKey{}, store)
}

func outputPersistence(ctx context.Context) OutputPersistence {
	store, _ := ctx.Value(outputPersistenceKey{}).(OutputPersistence)
	return store
}

func (s *OutputStore) retain(ctx context.Context, text string) (handle string, ok, saved bool) {
	handle, ok = s.put(text)
	if !ok {
		return
	}
	if store := outputPersistence(ctx); store != nil {
		saveCtx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		saved = store.SaveToolOutput(saveCtx, handle, text) == nil
	}
	return
}

func outputLifetime(saved bool) string {
	if saved {
		return "saved for later turns; bounded retention"
	}
	return "in memory only; valid during this run"
}

// load reads persistence only on a memory miss. Neither restoring a session nor
// building a prompt enumerates or loads these potentially large outputs.
func (s *OutputStore) load(ctx context.Context, handle string) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if s == nil {
			return "", fmt.Errorf("output store unavailable")
		}
		s.mu.Lock()
		if entry, ok := s.entries[handle]; ok {
			s.tick++
			entry.used = s.tick
			text := entry.text
			s.mu.Unlock()
			return text, nil
		}
		store := outputPersistence(ctx)
		if store == nil {
			s.mu.Unlock()
			return "", fmt.Errorf("unknown or expired output handle %q", handle)
		}
		if pending := s.loading[handle]; pending != nil {
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-pending.done:
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded) {
				// A different caller owned the canceled read. Retry with our own
				// still-live context rather than inheriting its cancellation.
				continue
			}
			return pending.text, pending.err
		}
		pending := &outputLoad{done: make(chan struct{})}
		if s.loading == nil {
			s.loading = make(map[string]*outputLoad)
		}
		s.loading[handle] = pending
		s.mu.Unlock()

		return s.restore(ctx, store, handle, pending)
	}
}

// restore publishes completion even if an embedding backend panics. The owner's
// panic still propagates, but other callers and later reads must not hang.
func (s *OutputStore) restore(ctx context.Context, store OutputPersistence, handle string, pending *outputLoad) (text string, err error) {
	completed := false
	defer func() {
		if !completed {
			text, err = "", fmt.Errorf("stored output load interrupted")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err == nil {
			if s.entries == nil {
				s.entries = make(map[string]*storedOutput)
			}
			s.tick++
			if entry, ok := s.entries[handle]; ok {
				text = entry.text
				entry.used = s.tick
			} else {
				s.entries[handle] = &storedOutput{text: text, used: s.tick}
				s.bytes += len(text)
				s.evictLocked()
			}
		}
		pending.text, pending.err = text, err
		delete(s.loading, handle)
		close(pending.done)
	}()
	text, err = store.ReadToolOutput(ctx, handle)
	if err == nil && len(text) > outputStoreBytes {
		err = fmt.Errorf("stored output exceeds the read limit")
	}
	if err != nil {
		text = ""
	}
	completed = true
	return text, err
}

// One in-flight persistence read per immutable handle. Completed calls leave no
// entry here; errors are not cached, and unrelated handles load concurrently.
type outputLoad struct {
	done chan struct{}
	text string
	err  error
}
