package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ApprovalsConf is operator-owned policy for the host approval gate. It is
// read only from the global <data-dir>/config.toml (LoadApprovals), never from
// a project config, so a repository cannot pre-approve its own actions.
type ApprovalsConf struct {
	// AutoApprove lists scopes approved without a prompt, also in batch runs:
	// "tool:operation[:account[:folder[:destination]]]", "*" = any segment,
	// a shorter pattern covers every longer scope it prefixes. Example:
	// auto_approve = ["thunderbird_mail:copy:account2"]
	AutoApprove []string `toml:"auto_approve"`
	// SessionGrantMinutes bounds "approve similar in this session" (default 30).
	SessionGrantMinutes int `toml:"session_grant_minutes"`
}

// LoadApprovals reads [approvals] from the global data-dir config.
func LoadApprovals(dataDir string) (ApprovalsConf, error) {
	if strings.TrimSpace(dataDir) == "" {
		return ApprovalsConf{}, fmt.Errorf("data directory is required for approvals policy")
	}
	cfg, err := LoadToml(filepath.Join(dataDir, "config.toml"))
	return cfg.Approvals, err
}
