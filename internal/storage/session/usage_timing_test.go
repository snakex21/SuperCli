package session

import (
	"context"
	"testing"
	"time"
)

func TestUsageTimingSurvivesReopenAndDeletedConversation(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sess := mustCreateUsageSession(t, store, "/timing")
	for _, record := range []UsageRecord{
		{DurationMS: 6000, TTFTMS: 2000, HasTiming: true},
		{TTFTMS: 3000}, // A historical first-output time is not a duration.
		{DurationMS: 1000, TTFTMS: 2000, HasTiming: true}, // Invalid clocks.
	} {
		record.SessionID, record.Input, record.Output = sess.ID, 20, 10
		record.PriceSnapshot = billingSnapshot(2)
		if err := store.AppendUsage(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	read, err := store.ReadUsage(ctx, sess.ID)
	if err != nil || len(read) != 3 || read[0].DurationMS != 6000 || !read[0].HasTiming || read[1].HasTiming || read[2].HasTiming {
		t.Fatalf("raw clocks: %+v, %v", read, err)
	}
	if err := store.Delete(sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var got []UsageRecord
	if err := store.VisitTokenUsage(ctx, "", func(u UsageRecord) { got = append(got, u) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].DurationMS != 6000 || got[0].TTFTMS != 2000 || !got[0].HasTiming || got[1].HasTiming || got[2].HasTiming {
		t.Fatalf("durable clocks: %+v", got)
	}
	for _, u := range got {
		if u.PriceSnapshot != nil || u.Input != 20 || u.Output != 10 {
			t.Fatalf("token projection changed usage: %+v", u)
		}
	}
	priced := collectBilling(t, store, "", time.Time{})
	for _, u := range priced {
		if u.PriceSnapshot == nil || *u.PriceSnapshot.AmountUSD != 2 {
			t.Fatalf("clock fields changed frozen price: %+v", u)
		}
	}
}

func TestOldUsageSchemaDoesNotInventDurations(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess := mustCreateUsageSession(t, store, "/old-timing")
	if err := store.AppendUsage(context.Background(), UsageRecord{SessionID: sess.ID, Input: 100, Output: 30, TTFTMS: 1234}); err != nil {
		t.Fatal(err)
	}
	for table, columns := range map[string][]string{"session_usage": {"duration_ms", "has_timing"}, "billing_usage": {"duration_ms", "ttft_ms", "has_timing"}} {
		for _, col := range columns {
			if _, err := store.db.Exec(`ALTER TABLE ` + table + ` DROP COLUMN ` + col); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.ReadUsage(context.Background(), sess.ID)
	if err != nil || len(rows) != 1 || rows[0].TTFTMS != 1234 || rows[0].HasTiming || rows[0].DurationMS != 0 {
		t.Fatalf("legacy raw clocks guessed: %+v, %v", rows, err)
	}
	journal := collectBilling(t, store, sess.ID, time.Time{})
	if len(journal) != 1 || journal[0].HasTiming || journal[0].DurationMS != 0 {
		t.Fatalf("legacy journal clocks guessed: %+v", journal)
	}
}
