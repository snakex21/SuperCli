package checkpoint

import "context"

// Requires Turn.mu and the store transaction held by record admission.
// The scoped capture and its diff can prove that no unrelated entries exist,
// avoiding another enumeration of both trees. Uncertain cases still narrow.
func (t *Turn) recordSnapshotsLocked(ctx context.Context, before, after string, files []string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if validNarrowOID(before) && validNarrowOID(after) && t.snapshotsAlreadyMinimal(files) {
		return before, after, nil
	}
	return t.manager.narrowChangedSnapshotsLocked(ctx, before, after, files)
}

// Scoped captures contain only entries at/below scopeRoots on both sides,
// including the first baseline retained while that scope grows. If every root
// is an exact changed file path, it is a file on at least one side. That side
// cannot also have children, so any children on the opposite side of a D/F
// change must all occur in the same diff. Thus every captured entry is selected.
// A directory that stays a directory, an untouched backup, or a whole capture
// cannot meet this proof and uses the existing narrowing path instead.
func (t *Turn) snapshotsAlreadyMinimal(files []string) bool {
	if !t.touched || t.wholeWorkspace || len(t.scopeRoots) == 0 {
		return false
	}
	selected, err := narrowSelectedPaths(files)
	if err != nil || len(selected) == 0 {
		return false
	}
	// narrowSelectedPaths centrally rejects invalid UTF-8 JSON paths.
	for _, root := range t.scopeRoots {
		if !selected[root] {
			return false
		}
	}
	return true
}
