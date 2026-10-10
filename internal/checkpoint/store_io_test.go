package checkpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTwoManagers(t *testing.T) (*Manager, *Manager) {
	t.Helper()
	home, data := t.TempDir(), t.TempDir()
	a, err := Open(home, data)
	if errors.Is(err, ErrUnavailable) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(home, data)
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestStoreAppendReloadsRecordsWrittenByAnotherManager(t *testing.T) {
	a, b := openTwoManagers(t)
	for _, step := range []struct {
		manager *Manager
		id      string
	}{{a, "first"}, {b, "second"}, {a, "third"}} {
		if err := step.manager.append(Record{ID: step.id, SessionID: "session"}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(a.meta)
	if err != nil {
		t.Fatal(err)
	}
	var records []Record
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0].ID != "first" || records[1].ID != "second" || records[2].ID != "third" {
		t.Fatalf("a stale manager lost another writer's checkpoints: %+v", records)
	}
}

func TestStoreForgetDoesNotResurrectRecordsInStaleManager(t *testing.T) {
	a, b := openTwoManagers(t)
	if err := a.append(Record{ID: "removed", SessionID: "session", UserSeq: 2}); err != nil {
		t.Fatal(err)
	}
	if err := b.append(Record{ID: "retained", SessionID: "other", UserSeq: 1}); err != nil {
		t.Fatal(err)
	}
	if err := a.ForgetFrom("session", 2); err != nil {
		t.Fatal(err)
	}
	if err := b.append(Record{ID: "next", SessionID: "other", UserSeq: 2}); err != nil {
		t.Fatal(err)
	}
	if got := b.Latest("session"); got != nil {
		t.Fatalf("discarded record resurrected: %+v", got)
	}
	if len(b.records) != 2 || b.records[0].ID != "retained" || b.records[1].ID != "next" {
		t.Fatalf("forget replaced another manager's retained records: %+v", b.records)
	}
}

func TestStoreRefusesMalformedMetadataAfterManagerWasOpened(t *testing.T) {
	a, b := openTwoManagers(t)
	original := []byte(`[{"id":"protected"},`)
	if err := os.WriteFile(a.meta, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := b.append(Record{ID: "must-not-replace"}); err == nil {
		t.Fatal("append accepted broken records after Open")
	}
	if err := b.ForgetFrom("session", 1); err == nil {
		t.Fatal("forget accepted broken records after Open")
	}
	if _, err := b.capture(context.Background()); err == nil {
		t.Fatal("capture accepted broken records after Open")
	}
	actual, err := os.ReadFile(a.meta)
	if err != nil || !bytes.Equal(actual, original) {
		t.Fatalf("broken metadata was replaced: %q, %v", actual, err)
	}
	if _, err := os.Stat(b.repo); !os.IsNotExist(err) {
		t.Fatalf("capture created repository despite invalid metadata: %v", err)
	}
}

func TestStoreClearCannotBeUndoneByStaleAppend(t *testing.T) {
	a, b := openTwoManagers(t)
	if err := b.append(Record{ID: "old", SessionID: "session"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Clear(); err != nil {
		t.Fatal(err)
	}
	// New records must work after Clear recreated the metadata directory, but
	// a cached list from a different engine must not resurrect deleted history.
	if err := os.MkdirAll(filepath.Dir(b.meta), 0700); err != nil {
		t.Fatal(err)
	}
	if err := b.append(Record{ID: "new", SessionID: "session"}); err != nil {
		t.Fatal(err)
	}
	if len(b.records) != 1 || b.records[0].ID != "new" {
		t.Fatalf("clear resurrected stale metadata: %+v", b.records)
	}
}
