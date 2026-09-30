package memory

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func BenchmarkNearestMemoryVectors(b *testing.B) {
	s, err := OpenStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	s.SetEmbedder(&fakeEmbedder{})
	rng := rand.New(rand.NewSource(7))
	q := make([]float32, 384)
	for i := range q {
		q[i] = rng.Float32()
	}
	tx, err := s.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	stmt, err := tx.Prepare("INSERT INTO memory_vectors(id,dim,vec) VALUES(?,?,?)")
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 4096; i++ {
		v := make([]float32, len(q))
		for j := range v {
			v[j] = rng.Float32()
		}
		if _, err := stmt.Exec(fmt.Sprintf("v%04d", i), len(v), encodeVec(v)); err != nil {
			b.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	for _, k := range []int{10, 128} {
		for _, mode := range []string{"reference", "current"} {
			b.Run(fmt.Sprintf("%d/%s", k, mode), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					lookup := s.nearestIDs
					if mode == "reference" {
						lookup = s.nearestIDsReference
					}
					ids, err := lookup(q, k, false)
					if err != nil || len(ids) != k {
						b.Fatal(err, len(ids))
					}
				}
			})
		}
	}
}
func TestNearestMemoryVectorsPreserveRankingAndTies(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetEmbedder(&fakeEmbedder{})
	rng := rand.New(rand.NewSource(17))
	q := []float32{1, 0.4, -0.8}
	type score struct {
		id    string
		value float64
	}
	reference := []score{}
	for i := 0; i < 137; i++ {
		id := fmt.Sprintf("v%03d", i)
		v := []float32{rng.Float32(), rng.Float32(), rng.Float32()}
		if i%9 == 0 {
			v = []float32{1, 0.4, -0.8}
		}
		if i%11 == 0 {
			v = []float32{0, 0, 0}
		}
		if _, err := s.db.Exec("INSERT INTO memory_vectors(id,dim,vec) VALUES(?,?,?)", id, len(v), encodeVec(v)); err != nil {
			t.Fatal(err)
		}
		reference = append(reference, score{id, cosine(q, v)})
	}
	if _, err := s.db.Exec("INSERT INTO memory_vectors(id,dim,vec) VALUES('wrong-dimension',2,?)", encodeVec([]float32{1, 1})); err != nil {
		t.Fatal(err)
	}
	sort.Slice(reference, func(i, j int) bool {
		if reference[i].value != reference[j].value {
			return reference[i].value > reference[j].value
		}
		return reference[i].id < reference[j].id
	})
	for _, k := range []int{1, 3, 10, 128, 500} {
		got, err := s.nearestIDs(q, k, false)
		if err != nil {
			t.Fatal(err)
		}
		want := make([]string, min(k, len(reference)))
		for i := range want {
			want[i] = reference[i].id
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("k=%d got=%v want=%v", k, got, want)
		}
	}
}

func (s *Store) nearestIDsReference(q []float32, k int, recall bool) ([]string, error) {
	query := `SELECT id, dim, vec FROM memory_vectors`
	if recall {
		query = `SELECT v.id, v.dim, v.vec FROM memory_vectors v JOIN memory_entries e ON e.id = v.id WHERE 1=1` + recallNoiseFilter
	}
	rows, err := s.db.Query(query)
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
