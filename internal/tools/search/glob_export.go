package search

import "context"

// Glob is the relative include glob shared by search_code and edit_files:
// `/`-separated segments, `**` for any depth, `{a,b}` alternatives, and a
// bare basename pattern matching at any depth.
type Glob struct{ g *searchGlob }

// CompileGlob validates and compiles one include glob. An empty pattern
// returns nil, which matches everything.
func CompileGlob(pattern string) (*Glob, error) {
	g, err := compileSearchGlob(pattern)
	if err != nil || g == nil {
		return nil, err
	}
	return &Glob{g: g}, nil
}

// Match reports whether file (under root) matches. A nil Glob matches all.
func (g *Glob) Match(root, file string) bool {
	if g == nil {
		return true
	}
	return g.g.matches(root, file)
}

// CanDescend reports whether any file below dir could still match.
func (g *Glob) CanDescend(root, dir string) bool {
	if g == nil {
		return true
	}
	return g.g.canDescend(root, dir)
}

// WalkFilesFiltered is the shared ignore-aware walk with a subtree filter:
// descend may reject a directory before its entries are read.
func WalkFilesFiltered(ctx context.Context, root string, descend func(dir string) bool, fn func(path string) error) error {
	return walkFilesFiltered(ctx, root, descend, fn)
}
