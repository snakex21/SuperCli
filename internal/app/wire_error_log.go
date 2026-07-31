package app

import (
	"log"
	"os"
	"path/filepath"

	"supercli/internal/tools"
)

// openToolErrorLog opens <dataDir>/logs/tool_errors.log, the NDJSON
// record of attributed tool failures. Every front-end that runs an
// agent loop shares this one path so the log stays a single corpus.
// Failures are non-fatal and return nil: the diagnostic log must
// never stop an agent from running. The caller owns Close.
func openToolErrorLog(dataDir string) *tools.ErrorLog {
	logsDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		log.Printf("mkdir logs: %v", err)
		return nil
	}
	errorLog, err := tools.NewErrorLog(filepath.Join(logsDir, "tool_errors.log"))
	if err != nil {
		log.Printf("open error log: %v", err)
		return nil
	}
	return errorLog
}
