package checkpoint

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestRetentionDeduplicatesPhysicalPathsAndExpiresOldest(t *testing.T) {
	shared := RetentionUnit{"checkpoints/a/objects.git/objects/aa/shared", 80}
	unique := RetentionUnit{"checkpoints/a/objects.git/objects/bb/unique", 50}
	old := time.Unix(1, 0)
	in := RetentionInventory{Complete: true, Evidence: "fresh", Fixed: []RetentionUnit{{"checkpoints/a/turns.json", 10}}, Records: []RetentionRecord{
		{Key: RetentionKey{"checkpoints/a", "new"}, CreatedAt: old.Add(2 * time.Second), Order: 2, Units: []RetentionUnit{shared}},
		{Key: RetentionKey{"checkpoints/a", "middle"}, CreatedAt: old.Add(time.Second), Order: 1, Units: []RetentionUnit{unique}},
		{Key: RetentionKey{"checkpoints/a", "old"}, CreatedAt: old, Order: 0, Units: []RetentionUnit{shared, shared}},
	}}
	plan, err := PlanStoreRetention(in, 90)
	if err != nil {
		t.Fatal(err)
	}
	if plan.BeforeBytes != 140 || plan.AfterBytes != 90 || plan.ProtectedBytes != 10 {
		t.Fatalf("incorrect physical accounting: %+v", plan)
	}
	want := []RetentionKey{{"checkpoints/a", "old"}, {"checkpoints/a", "middle"}}
	if !reflect.DeepEqual(plan.Expired, want) || len(plan.Kept) != 1 || plan.Kept[0].ID != "new" {
		t.Fatalf("oldest-first expiry: %+v", plan)
	}
}

func TestRetentionSameOIDInDifferentReposConsumesSpaceTwice(t *testing.T) {
	in := RetentionInventory{Complete: true, Evidence: "fresh", Records: []RetentionRecord{
		{Key: RetentionKey{"checkpoints/a", "old"}, Units: []RetentionUnit{{"checkpoints/a/objects.git/objects/aa/blob", 100}}},
		{Key: RetentionKey{"badcheckpoints/b", "new"}, CreatedAt: time.Unix(1, 0), Units: []RetentionUnit{{"badcheckpoints/b/objects.git/objects/aa/blob", 100}}},
	}}
	plan, err := PlanStoreRetention(in, 100)
	if err != nil || plan.BeforeBytes != 200 || plan.AfterBytes != 100 || len(plan.Expired) != 1 || plan.Expired[0].ID != "old" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestRetentionProtectedFloorNeverExpiresHistoryPointlessly(t *testing.T) {
	u := RetentionUnit{"checkpoints/a/objects.git/objects/aa/blob", 101}
	in := RetentionInventory{Complete: true, Evidence: "fresh", Fixed: []RetentionUnit{u}, Records: []RetentionRecord{{Key: RetentionKey{"checkpoints/a", "old"}, Units: []RetentionUnit{u}}}}
	plan, err := PlanStoreRetention(in, 100)
	if !errors.Is(err, ErrStoreBudget) || plan.ProtectedBytes != 101 || len(plan.Expired) != 0 || len(plan.Kept) != 1 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	in.Fixed = nil
	in.Records[0].Protected = true
	plan, err = PlanStoreRetention(in, 100)
	if !errors.Is(err, ErrStoreBudget) || plan.ProtectedBytes != 101 || len(plan.Expired) != 0 {
		t.Fatalf("active record lost: %+v %v", plan, err)
	}
}

func TestRetentionNoPressurePreservesAllAndTieOrderIsStable(t *testing.T) {
	in := RetentionInventory{Complete: true, Evidence: "fresh", Records: []RetentionRecord{
		{Key: RetentionKey{"checkpoints/a", "two"}, Order: 2, Units: []RetentionUnit{{"checkpoints/a/two", 10}}},
		{Key: RetentionKey{"checkpoints/a", "one"}, Order: 1, Units: []RetentionUnit{{"checkpoints/a/one", 10}}},
	}}
	plan, err := PlanStoreRetention(in, 20)
	if err != nil || len(plan.Expired) != 0 || len(plan.Kept) != 2 {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	plan, err = PlanStoreRetention(in, 10)
	if err != nil || len(plan.Expired) != 1 || plan.Expired[0].ID != "one" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestRetentionRejectsIncompleteUnsafeOrConflictingCosts(t *testing.T) {
	for _, in := range []RetentionInventory{
		{Evidence: "fresh"},
		{Complete: true},
		{Complete: true, Evidence: "fresh", Fixed: []RetentionUnit{{"../sessions.db", 10}}},
		{Complete: true, Evidence: "fresh", Fixed: []RetentionUnit{{"checkpoints/a", -1}}},
		{Complete: true, Evidence: "fresh", Fixed: []RetentionUnit{{"checkpoints/a", math.MaxInt64}, {"checkpoints/b", 1}}},
		{Complete: true, Evidence: "fresh", Fixed: []RetentionUnit{{"checkpoints/a", 1}, {"checkpoints/a", 2}}},
		{Complete: true, Evidence: "fresh", Records: []RetentionRecord{{Key: RetentionKey{"checkpoints/a", "x"}}, {Key: RetentionKey{"checkpoints/a", "x"}}}},
	} {
		if _, err := PlanStoreRetention(in, 100); !errors.Is(err, ErrStoreInventory) {
			t.Fatalf("unsafe inventory accepted: %+v: %v", in, err)
		}
	}
}

func TestStoreBudgetAdmissionIncludesTransientReserve(t *testing.T) {
	if err := CheckStoreBudgetAdmission(100, 80, 20); err != nil {
		t.Fatal(err)
	}
	if err := CheckStoreBudgetAdmission(100, 80, 21); !errors.Is(err, ErrStoreBudget) {
		t.Fatalf("temporary space was omitted: %v", err)
	}
	for _, args := range [][3]int64{{0, 0, 0}, {100, -1, 1}, {100, 1, -1}, {math.MaxInt64, math.MaxInt64, 1}} {
		if err := CheckStoreBudgetAdmission(args[0], args[1], args[2]); !errors.Is(err, ErrStoreInventory) {
			t.Fatalf("invalid admission accepted: %v: %v", args, err)
		}
	}
}
