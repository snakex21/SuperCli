package memory

import "context"

// Keep this predicate aligned with IsDiagnosticNoise. Filtering before LIMIT
// prevents legacy diagnostics from hiding useful matches further down the rank.
const recallNoiseFilter = " AND NOT (e.scope GLOB 'pattern:*' AND instr(lower(e.content), 'no heuristic matched') > 0)"

// RecallSearch applies the agent's diagnostic-noise policy before ranking caps.
// Search and HybridSearch remain unfiltered for inspection and maintenance.
func (s *Store) RecallSearch(ctx context.Context, query string, k int) ([]Entry, error) {
	return s.hybridSearch(ctx, query, k, true)
}

// RecallRecent supplies durable notes for a cross-language fallback. Patterns
// are relevant only through a matching search, and must not consume this limit.
func (s *Store) RecallRecent(n int) ([]Entry, error) {
	if n <= 0 {
		n = -1
	}
	rows, err := s.db.Query(`SELECT id, scope, file_path, line_start, line_end, content, tags, source, created_at, updated_at
  FROM memory_entries WHERE scope NOT GLOB 'pattern:*'
  ORDER BY updated_at DESC, created_at DESC, id LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAll(rows, 0)
}
