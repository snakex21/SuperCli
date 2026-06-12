package memory

import (
	"context"
	"encoding/binary"
	"math"
	"sort"
)

// Vector storage and hybrid (FTS5 + cosine) search.
//
// Vectors live in a plain SQLite table as little-endian float32
// BLOBs. We deliberately do not use sqlite-vec: it needs cgo (or a
// loadable extension), and the store runs on modernc.org/sqlite —
// pure Go. Memory databases are small (hundreds of entries), so a
// full scan + cosine in Go is plenty fast and keeps the build
// dependency-free.

// SetEmbedder attaches an embedding backend to the store and
// creates the vector table. Pass nil to disable. Safe to call
// from a goroutine after startup.
func (s *Store) SetEmbedder(e Embedder) {
	if s == nil {
		return
	}
	s.embedMu.Lock()
	s.embedder = e
	s.embedMu.Unlock()
	if e != nil {
		_, _ = s.db.Exec(`CREATE TABLE IF NOT EXISTS memory_vectors (
			id  TEXT PRIMARY KEY,
			dim INTEGER NOT NULL,
			vec BLOB NOT NULL
		)`)
	}
}

// EmbedderName returns the attached embedder's name, or "" when
// search is FTS5-only.
func (s *Store) EmbedderName() string {
	if s == nil {
		return ""
	}
	s.embedMu.RLock()
	defer s.embedMu.RUnlock()
	if s.embedder == nil {
		return ""
	}
	return s.embedder.Name()
}

func (s *Store) getEmbedder() Embedder {
	s.embedMu.RLock()
	defer s.embedMu.RUnlock()
	return s.embedder
}

// afterPut is the Put hook: best-effort embedding of the new
// entry. Failures are swallowed — FTS5 already indexed the text.
func (s *Store) afterPut(e Entry) {
	emb := s.getEmbedder()
	if emb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), embedTimeout)
	defer cancel()
	vec, err := emb.Embed(ctx, e.Content)
	if err != nil || len(vec) == 0 {
		return
	}
	_, _ = s.db.Exec(
		`INSERT OR REPLACE INTO memory_vectors(id, dim, vec) VALUES (?,?,?)`,
		e.ID, len(vec), encodeVec(vec),
	)
}

// afterDelete is the Delete hook: drop the entry's vector. A
// missing vector table (embedder never attached) is fine.
func (s *Store) afterDelete(id string) {
	_, _ = s.db.Exec(`DELETE FROM memory_vectors WHERE id = ?`, id)
}

// HybridSearch combines FTS5 and vector search with reciprocal
// rank fusion. Without an embedder (or on any embedding error) it
// degrades to plain FTS5 — hybrid search never fails harder than
// keyword search.
func (s *Store) HybridSearch(ctx context.Context, query string, k int) ([]Entry, error) {
	if k <= 0 {
		k = 5
	}
	ftsResults, err := s.Search(query, k*2)
	if err != nil {
		ftsResults = nil // degrade; vector side may still answer
	}
	emb := s.getEmbedder()
	if emb == nil {
		return capEntries(ftsResults, k), err
	}
	qvec, embErr := emb.Embed(ctx, query)
	if embErr != nil || len(qvec) == 0 {
		return capEntries(ftsResults, k), nil
	}
	vecIDs, vecErr := s.nearestIDs(qvec, k*2)
	if vecErr != nil || len(vecIDs) == 0 {
		return capEntries(ftsResults, k), nil
	}

	// Reciprocal rank fusion: score(id) = Σ 1/(60+rank).
	const rrfK = 60.0
	scores := map[string]float64{}
	byID := map[string]Entry{}
	for rank, e := range ftsResults {
		scores[e.ID] += 1.0 / (rrfK + float64(rank+1))
		byID[e.ID] = e
	}
	for rank, id := range vecIDs {
		scores[id] += 1.0 / (rrfK + float64(rank+1))
		if _, ok := byID[id]; !ok {
			if e, err := s.Get(id); err == nil {
				byID[id] = e
			}
		}
	}
	ids := make([]string, 0, len(scores))
	for id := range scores {
		if _, ok := byID[id]; ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] != scores[ids[j]] {
			return scores[ids[i]] > scores[ids[j]]
		}
		return ids[i] < ids[j] // deterministic tie-break
	})
	out := make([]Entry, 0, k)
	for _, id := range ids {
		out = append(out, byID[id])
		if len(out) == k {
			break
		}
	}
	return out, nil
}

// nearestIDs scans all stored vectors and returns the k IDs with
// the highest cosine similarity to q.
func (s *Store) nearestIDs(q []float32, k int) ([]string, error) {
	rows, err := s.db.Query(`SELECT id, dim, vec FROM memory_vectors`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type scored struct {
		id  string
		sim float64
	}
	var all []scored
	for rows.Next() {
		var id string
		var dim int
		var blob []byte
		if err := rows.Scan(&id, &dim, &blob); err != nil {
			return nil, err
		}
		v := decodeVec(blob)
		if len(v) != len(q) || len(v) != dim {
			continue // dimension mismatch (embedder changed) — skip
		}
		all = append(all, scored{id: id, sim: cosine(q, v)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].sim != all[j].sim {
			return all[i].sim > all[j].sim
		}
		return all[i].id < all[j].id
	})
	if len(all) > k {
		all = all[:k]
	}
	ids := make([]string, len(all))
	for i, s := range all {
		ids[i] = s.id
	}
	return ids, nil
}

func capEntries(es []Entry, k int) []Entry {
	if len(es) > k {
		return es[:k]
	}
	return es
}

// cosine returns the cosine similarity of two equal-length vectors.
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// encodeVec serialises a vector as little-endian float32 bytes.
func encodeVec(v []float32) []byte {
	out := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(f))
	}
	return out
}

// decodeVec is the inverse of encodeVec.
func decodeVec(b []byte) []float32 {
	n := len(b) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}
