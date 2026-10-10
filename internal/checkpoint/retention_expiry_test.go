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

func TestStoreRetentionExpiryFrontierIsMonotoneAndPreservesUnknownJSON(t *testing.T) {
	original := []byte(`{"version":1,"future":9007199254740993123,"sessions":{"keep":{"through_seq":7,"future":{"integer":9007199254740993123}},"other":{"through_seq":2,"unknown_sequence":true}}}`)
	data, err := retentionAdvanceExpiry(original, []Record{{SessionID: "keep", UserSeq: 3}, {SessionID: "keep", UserSeq: 9}, {SessionID: "legacy", UserSeq: 0}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := retentionDecodeExpiry(data)
	if err != nil || string(d.fields["future"]) != "9007199254740993123" || !bytes.Equal(d.sessions["keep"]["future"], []byte(`{"integer":9007199254740993123}`)) {
		t.Fatalf("frontier lost exact unknown JSON: %s %v", data, err)
	}
	keep, err := retentionDecodeExpirySession(d.sessions["keep"])
	if err != nil || keep.ThroughSeq != 9 || keep.UnknownSeq {
		t.Fatalf("non-monotone frontier: %+v %v", keep, err)
	}
	other, _ := retentionDecodeExpirySession(d.sessions["other"])
	legacy, _ := retentionDecodeExpirySession(d.sessions["legacy"])
	if !other.UnknownSeq || !legacy.UnknownSeq || legacy.ThroughSeq != 0 {
		t.Fatalf("unknown sequence boundary lost: %+v %+v", other, legacy)
	}
	for _, invalid := range []string{`null`, `{"version":1,"sessions":null}`, `{"version":1,"sessions":{"x":{"through_seq":null}}}`, `{"version":1,"sessions":{"x":{"through_seq":1,"unknown_sequence":null}}}`} {
		if _, err := retentionDecodeExpiry([]byte(invalid)); !errors.Is(err, ErrStoreInventory) {
			t.Fatalf("malformed boundary accepted: %s %v", invalid, err)
		}
	}
}

func retentionExpireFixtureOld(t *testing.T, m *Manager, data string, old Record) {
	t.Helper()
	a, err := AuditStoreRetentionLocked(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	store, err := filepath.Rel(data, filepath.Dir(m.meta))
	if err != nil {
		t.Fatal(err)
	}
	j, err := makeRetentionJournal(a, RetentionPlan{Expired: []RetentionKey{{Store: filepath.ToSlash(store), ID: old.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRetentionJournal(data, j); err != nil {
		t.Fatal(err)
	}
	if err := resumeRetentionJournalLocked(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if _, err := retireRetentionJournalLocked(data); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRetentionExpiryFrontierRefusesOldRewindBeforeAnyRestore(t *testing.T) {
	m, data, old, newest := retentionFixture(t)
	retentionTransaction(t, m, func() {
		retentionExpireFixtureOld(t, m, data, old)
		if err := m.requireRetentionHistoryFromLocked("session", 1); !errors.Is(err, ErrHistoryExpired) {
			t.Fatalf("expired boundary accepted: %v", err)
		}
		if err := m.requireRetentionHistoryFromLocked("session", 2); err != nil {
			t.Fatal("retained boundary refused:", err)
		}
	})
	before, err := os.ReadFile(filepath.Join(m.home, "new.bin"))
	if err != nil {
		t.Fatal(err)
	}
	// Integration contract: Manager.UndoFrom must invoke the locked guard before
	// preview/restore. This catches the original incomplete-suffix success bug.
	batch, err := m.UndoFrom(context.Background(), "session", 1)
	after, readErr := os.ReadFile(filepath.Join(m.home, "new.bin"))
	if !errors.Is(err, ErrHistoryExpired) || len(batch.Records) != 0 || readErr != nil || !bytes.Equal(before, after) {
		t.Fatalf("old rewind changed retained files: %+v err=%v read=%v", batch, err, readErr)
	}
	if _, err := m.Undo(context.Background(), newest.ID); err != nil {
		t.Fatal("exact retained ID undo must remain available:", err)
	}
}

func TestStoreRetentionLegacyExpiryUnknownSequenceIsConservative(t *testing.T) {
	m, data, old, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		raw, err := os.ReadFile(m.meta)
		if err != nil {
			t.Fatal(err)
		}
		var records []Record
		if err := json.Unmarshal(raw, &records); err != nil {
			t.Fatal(err)
		}
		records[0].UserSeq = 0
		raw, err = json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(m.meta, raw, 0600); err != nil {
			t.Fatal(err)
		}
		retentionExpireFixtureOld(t, m, data, old)
		for _, seq := range []int{1, 2, 1000} {
			if err := m.requireRetentionHistoryFromLocked("session", seq); !errors.Is(err, ErrHistoryExpired) {
				t.Fatalf("legacy unknown boundary accepted seq=%d: %v", seq, err)
			}
		}
		if err := m.requireRetentionHistoryFromLocked("other-session", 1); err != nil {
			t.Fatal("unrelated session affected:", err)
		}
	})
}

func TestStoreRetentionJournalPersistsFrontierBeforeMetadataAndResumes(t *testing.T) {
	m, data, old, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		a, err := AuditStoreRetentionLocked(context.Background(), data)
		if err != nil {
			t.Fatal(err)
		}
		store, _ := filepath.Rel(data, filepath.Dir(m.meta))
		j, err := makeRetentionJournal(a, RetentionPlan{Expired: []RetentionKey{{Store: filepath.ToSlash(store), ID: old.ID}}})
		if err != nil || writeRetentionJournal(data, j) != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(m.meta)
		if err != nil {
			t.Fatal(err)
		}
		// The earliest interruption point already refuses a partial rewind, but
		// every original record and blob still exists until roll-forward.
		if err := retentionAtomicWrite(data, filepath.Join(filepath.Dir(m.meta), retentionExpiryName), j.Repos[0].ExpiryNext); err != nil {
			t.Fatal(err)
		}
		current, err := os.ReadFile(m.meta)
		if err != nil || !bytes.Equal(current, original) {
			t.Fatal("early frontier write changed metadata:", err)
		}
		if err := m.requireRetentionHistoryFromLocked("session", 1); !errors.Is(err, ErrHistoryExpired) {
			t.Fatal("early frontier did not refuse old rewind:", err)
		}
		if err := resumeRetentionJournalLocked(context.Background(), data); err != nil {
			t.Fatal(err)
		}
		if err := resumeRetentionJournalLocked(context.Background(), data); err != nil {
			t.Fatal("journal is not idempotent:", err)
		}
		current, err = os.ReadFile(m.meta)
		if err != nil || !bytes.Equal(current, j.Repos[0].Next) {
			t.Fatal("journal did not roll forward exact metadata:", err)
		}
	})
}

func TestStoreRetentionForgetFrontierPermitsReusedSequences(t *testing.T) {
	m, data, _, _ := retentionFixture(t)
	retentionTransaction(t, m, func() {
		path := filepath.Join(filepath.Dir(m.meta), retentionExpiryName)
		frontier, err := retentionAdvanceExpiry(nil, []Record{{SessionID: "session", UserSeq: 9}, {SessionID: "legacy", UserSeq: 0}})
		if err != nil || retentionAtomicWrite(data, path, frontier) != nil {
			t.Fatal(err)
		}
		if err := m.forgetRetentionHistoryFromLocked("session", 5); err != nil {
			t.Fatal(err)
		}
		if err := m.requireRetentionHistoryFromLocked("session", 5); err != nil {
			t.Fatal("old frontier blocked reused message seq=5:", err)
		}
		if err := m.requireRetentionHistoryFromLocked("session", 4); !errors.Is(err, ErrHistoryExpired) {
			t.Fatal("forget fabricated exact earlier expiry membership:", err)
		}
		if err := m.forgetRetentionHistoryFromLocked("legacy", 5); err != nil {
			t.Fatal(err)
		}
		if err := m.requireRetentionHistoryFromLocked("legacy", 5); !errors.Is(err, ErrHistoryExpired) {
			t.Fatal("partial truncate discarded unknown legacy boundary:", err)
		}
		if err := m.forgetRetentionHistoryFromLocked("legacy", 1); err != nil {
			t.Fatal(err)
		}
		if err := m.requireRetentionHistoryFromLocked("legacy", 1); err != nil {
			t.Fatal("full truncate failed to clear legacy frontier:", err)
		}
		if err := m.forgetRetentionHistoryFromLocked("session", 1); err != nil {
			t.Fatal(err)
		}
		if err := m.requireRetentionHistoryFromLocked("session", 1); err != nil {
			t.Fatal("full truncate blocked reused first message:", err)
		}
	})
}
