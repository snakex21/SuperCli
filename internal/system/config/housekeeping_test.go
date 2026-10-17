package config

import (
	"path/filepath"
	"testing"
)

func TestHousekeepingDefaultsAndToml(t *testing.T) {
	c := HousekeepingConfig{}.Defaults()
	if c.CheckpointKeepPerSession != 20 || c.CheckpointMaxAgeDays != 14 || c.SnapshotMaxAgeDays != 30 || c.SnapshotMaxBytes != 512<<20 || c.VacuumIntervalDays != 7 || c.VacuumFreePercent != 25 || c.VacuumMinBytes != 64<<20 {
		t.Fatalf("defaults=%+v", c)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	disabled := false
	want := TomlConfig{Housekeeping: HousekeepingConfig{Enabled: &disabled, CheckpointKeepPerSession: 5, VacuumIntervalDays: 3}}
	if err := SaveToml(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadToml(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Housekeeping.Enabled == nil || *got.Housekeeping.Enabled || got.Housekeeping.CheckpointKeepPerSession != 5 || got.Housekeeping.VacuumIntervalDays != 3 {
		t.Fatalf("config=%+v", got.Housekeeping)
	}
	merged := TomlConfig{Housekeeping: c}
	mergeToml(&merged, want)
	if merged.Housekeeping.CheckpointKeepPerSession != 5 || merged.Housekeeping.SnapshotMaxBytes != 512<<20 {
		t.Fatalf("merge=%+v", merged.Housekeeping)
	}
}
