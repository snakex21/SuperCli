package config

// HousekeepingConfig zero values select conservative defaults. Negative values
// disable the corresponding retention/vacuum task. Enabled=false disables all.
type HousekeepingConfig struct {
	Enabled                  *bool `toml:"enabled"`
	CheckpointKeepPerSession int   `toml:"checkpoint_keep_per_session"`
	CheckpointMaxAgeDays     int   `toml:"checkpoint_max_age_days"`
	SnapshotMaxAgeDays       int   `toml:"snapshot_max_age_days"`
	SnapshotMaxBytes         int64 `toml:"snapshot_max_bytes"`
	VacuumIntervalDays       int   `toml:"vacuum_interval_days"`
	VacuumFreePercent        int   `toml:"vacuum_free_percent"`
	VacuumMinBytes           int64 `toml:"vacuum_min_bytes"`
}

func (c HousekeepingConfig) Defaults() HousekeepingConfig {
	if c.CheckpointKeepPerSession == 0 {
		c.CheckpointKeepPerSession = 20
	}
	if c.CheckpointMaxAgeDays == 0 {
		c.CheckpointMaxAgeDays = 14
	}
	if c.SnapshotMaxAgeDays == 0 {
		c.SnapshotMaxAgeDays = 30
	}
	if c.SnapshotMaxBytes == 0 {
		c.SnapshotMaxBytes = 512 << 20
	}
	if c.VacuumIntervalDays == 0 {
		c.VacuumIntervalDays = 7
	}
	if c.VacuumFreePercent == 0 {
		c.VacuumFreePercent = 25
	}
	if c.VacuumFreePercent > 100 {
		c.VacuumFreePercent = 100
	}
	if c.VacuumMinBytes == 0 {
		c.VacuumMinBytes = 64 << 20
	}
	return c
}

func mergeHousekeeping(dst *HousekeepingConfig, src HousekeepingConfig) {
	if src.Enabled != nil {
		dst.Enabled = src.Enabled
	}
	if src.CheckpointKeepPerSession != 0 {
		dst.CheckpointKeepPerSession = src.CheckpointKeepPerSession
	}
	if src.CheckpointMaxAgeDays != 0 {
		dst.CheckpointMaxAgeDays = src.CheckpointMaxAgeDays
	}
	if src.SnapshotMaxAgeDays != 0 {
		dst.SnapshotMaxAgeDays = src.SnapshotMaxAgeDays
	}
	if src.SnapshotMaxBytes != 0 {
		dst.SnapshotMaxBytes = src.SnapshotMaxBytes
	}
	if src.VacuumIntervalDays != 0 {
		dst.VacuumIntervalDays = src.VacuumIntervalDays
	}
	if src.VacuumFreePercent != 0 {
		dst.VacuumFreePercent = src.VacuumFreePercent
	}
	if src.VacuumMinBytes != 0 {
		dst.VacuumMinBytes = src.VacuumMinBytes
	}
}
