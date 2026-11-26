package childproc

import (
	"path/filepath"
	"strings"
)

// SweepOrphanedCheckpointGit ends git processes left behind by an earlier
// SuperCli of this install: a git.exe whose parent is gone and whose command
// line names this data directory's checkpoint store. Nothing else matches:
// not the user's own git, not a git with a live parent. Returns how many
// were ended. A no-op outside Windows (process groups end with the owner).
func SweepOrphanedCheckpointGit(dataDir string) int {
	if dataDir == "" {
		return 0
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return 0
	}
	marker := strings.ToLower(filepath.Join(abs, "checkpoints") + string(filepath.Separator))
	return sweepOrphanedGit(func(commandLine string) bool {
		return strings.Contains(strings.ToLower(commandLine), marker)
	})
}
