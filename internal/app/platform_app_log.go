package app

import (
	"log"
	"os"
	"path/filepath"
)

func initAppLog(dataDir string) *os.File {
	logsDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(logsDir, "supercli.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	log.SetOutput(f)
	return f
}
