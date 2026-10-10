package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const projectCheckpointCleanupFile = "project-cleanup.json"

type projectCheckpointCleanupPreference struct {
	DeleteOnRemove bool `json:"delete_checkpoints_on_remove"`
}

// Shared by GUI and CLI. A missing preference preserves checkpoints; only an
// explicit human choice enables removal. Corrupt/oversized state fails closed.
func LoadProjectCheckpointCleanup(dataDir string) (bool, error) {
	f, err := os.Open(filepath.Join(dataDir, projectCheckpointCleanupFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return false, fmt.Errorf("project checkpoint cleanup preference must be a regular file of at most 4096 bytes")
	}
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	var preference projectCheckpointCleanupPreference
	if err := d.Decode(&preference); err != nil {
		return false, fmt.Errorf("project checkpoint cleanup preference: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("project checkpoint cleanup preference contains extra data")
	}
	return preference.DeleteOnRemove, nil
}

func SaveProjectCheckpointCleanup(dataDir string, enabled bool) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(projectCheckpointCleanupPreference{DeleteOnRemove: enabled})
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dataDir, projectCheckpointCleanupFile), append(raw, '\n'))
}
