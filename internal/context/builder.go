package context

import (
	"context"
	"fmt"
)

// Loader is the contract every context source loader implements.
// Loaders are stateless: they read from disk/DB/env when Load
// is called and return a fresh Source.
type Loader interface {
	Name() string
	Priority() int
	Load() (Source, error)
}

// Builder aggregates a set of loaders and produces a Sources
// collection on demand. It is the entry point used by main.go
// and the agent loop.
type Builder struct {
	loaders []Loader
}

// NewBuilder returns a builder with no loaders.
func NewBuilder() *Builder {
	return &Builder{}
}

// Add appends a loader. Duplicate names are rejected because
// the resulting Sources would be ambiguous.
func (b *Builder) Add(l Loader) error {
	if l == nil {
		return fmt.Errorf("context.Builder.Add: loader is nil")
	}
	for _, existing := range b.loaders {
		if existing.Name() == l.Name() {
			return fmt.Errorf("context.Builder.Add: duplicate loader name %q", l.Name())
		}
	}
	b.loaders = append(b.loaders, l)
	return nil
}

// MustAdd is the panicking variant.
func (b *Builder) MustAdd(l Loader) {
	if err := b.Add(l); err != nil {
		panic(err)
	}
}

// Build calls Load on every loader, collects the results, and
// returns them in declared order. Loaders that return an error
// are skipped (the error is reported via the returned error
// list, but the build does not abort on a single failure).
//
// Empty-Body sources (e.g. no CLAUDE.md present) are kept so
// callers can see the structure; the caller may drop them.
func (b *Builder) Build(ctx context.Context) (Sources, []error) {
	sources := NewSources()
	var errs []error
	for _, l := range b.loaders {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		s, err := l.Load()
		if err != nil {
			errs = append(errs, fmt.Errorf("loader %q: %w", l.Name(), err))
			continue
		}
		sources.Append(s)
	}
	return sources, errs
}

// Names returns the registered loader names in order.
func (b *Builder) Names() []string {
	out := make([]string, 0, len(b.loaders))
	for _, l := range b.loaders {
		out = append(out, l.Name())
	}
	return out
}
