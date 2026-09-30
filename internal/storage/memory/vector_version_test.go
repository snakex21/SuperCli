package memory

import (
	"reflect"
	"testing"
	"time"
)

func TestVectorSaveChecksCurrentSourceVersionAtomically(t *testing.T) {
	for _, change := range []string{"delete", "content", "timestamp", "current"} {
		t.Run(change, func(t *testing.T) {
			store, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			original := Entry{ID: "versioned", Scope: ScopeFact, Content: "original source", Source: SourceAgent, UpdatedAt: time.Unix(1700000000, 0)}
			if err := store.Put(original); err != nil {
				t.Fatal(err)
			}
			saved, err := store.Get(original.ID)
			if err != nil {
				t.Fatal(err)
			}
			store.SetEmbedder(&fakeEmbedder{vectors: map[string][]float32{}})
			store.SetEmbedder(nil) // Use the vector schema without a background indexing race.
			current := saved
			if change == "delete" {
				if err := store.Delete(saved.ID); err != nil {
					t.Fatal(err)
				}
			} else if change != "current" {
				if change == "content" {
					current.Content = "new source"
				} else {
					current.UpdatedAt = current.UpdatedAt.Add(time.Second)
				}
				if err := store.Put(current); err != nil {
					t.Fatal(err)
				}
				if change == "timestamp" {
					// Put owns timestamps; simulate a later persisted source version.
					if _, err := store.db.Exec("UPDATE memory_entries SET updated_at=? WHERE id=?", saved.UpdatedAt.Add(time.Second).Unix(), saved.ID); err != nil {
						t.Fatal(err)
					}
				}
				current, err = store.Get(saved.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.saveEntryVector(current, []float32{0, 1, 0}); err != nil {
					t.Fatal(err)
				}
			}
			// This is the old embedding completion, carrying the previously validated
			// source snapshot. It must not recreate a deleted ID or replace a new vector.
			if err := store.saveEntryVector(saved, []float32{1, 0, 0}); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := store.db.QueryRow("SELECT COUNT(*) FROM memory_vectors WHERE id=?", saved.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if change == "delete" {
				if count != 0 {
					t.Fatalf("deleted vector resurrected: %d", count)
				}
				ids, err := store.nearestIDs([]float32{1, 0, 0}, 1, false)
				if err != nil || len(ids) != 0 {
					t.Fatalf("deleted ID returned: %v %v", ids, err)
				}
				return
			}
			if count != 1 {
				t.Fatalf("current source lost its vector: %d", count)
			}
			var raw []byte
			if err := store.db.QueryRow("SELECT vec FROM memory_vectors WHERE id=?", saved.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			want := []float32{0, 1, 0}
			if change == "current" {
				want = []float32{1, 0, 0}
			}
			if got := decodeVec(raw); !reflect.DeepEqual(got, want) {
				t.Fatalf("stale vector overwrite: got %v want %v", got, want)
			}
		})
	}
}
