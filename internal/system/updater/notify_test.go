package updater

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCachedCheckReusesDailyResultPerVersion(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	check := func(context.Context) (State, error) {
		calls++
		return State{LatestVersion: "9.9.9", Available: true, Status: "available"}, nil
	}
	now := time.Unix(1_800_000_000, 0)
	ctx := context.Background()
	if s, err := cachedCheck(ctx, dir, "1.0.0", now, check); err != nil || !s.Available || calls != 1 {
		t.Fatalf("first: %+v %v calls=%d", s, err, calls)
	}
	if s, err := cachedCheck(ctx, dir, "1.0.0", now.Add(23*time.Hour), check); err != nil || s.LatestVersion != "9.9.9" || calls != 1 {
		t.Fatalf("within a day must reuse: %+v %v calls=%d", s, err, calls)
	}
	if _, err := cachedCheck(ctx, dir, "1.0.0", now.Add(25*time.Hour), check); err != nil || calls != 2 {
		t.Fatalf("after a day must recheck: calls=%d", calls)
	}
	if _, err := cachedCheck(ctx, dir, "1.0.1", now.Add(25*time.Hour), check); err != nil || calls != 3 {
		t.Fatalf("a new installed version must recheck: calls=%d", calls)
	}
	failing := func(context.Context) (State, error) { calls++; return State{}, errors.New("offline") }
	other := t.TempDir()
	for i := 0; i < 2; i++ {
		if _, err := cachedCheck(ctx, other, "1.0.0", now, failing); err == nil {
			t.Fatal("failure was hidden")
		}
	}
	if calls != 5 {
		t.Fatalf("failures must not be cached: calls=%d", calls)
	}
}
