package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

// crashLogPath returns the path to the crash log file inside the
// resolved data directory (portable: supercli-data next to the exe).
func crashLogPath(dataDir string) string {
	return filepath.Join(dataDir, "logs", "crash.log")
}

// logCrash writes a panic stack trace to the crash log.
// Safe to call from a defer recover() block.
func logCrash(dataDir string, r any) {
	path := crashLogPath(dataDir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CRASH: cannot open crash log: %v\n", err)
		return
	}
	defer f.Close()

	now := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(f, "=== CRASH %s ===\n", now)
	fmt.Fprintf(f, "panic: %v\n", r)
	fmt.Fprintf(f, "stack:\n%s\n", string(debug.Stack()))
	fmt.Fprintf(f, "=== END ===\n\n")
	_ = f.Sync()
}

// recoverAndLog is a defer helper for goroutines. Usage:
//
//	defer recoverAndLog(dataDir)()
func recoverAndLog(dataDir string) func() {
	return func() {
		if r := recover(); r != nil {
			logCrash(dataDir, r)
			fmt.Fprintf(os.Stderr, "CRASH: %v (logged to %s)\n", r, crashLogPath(dataDir))
		}
	}
}
