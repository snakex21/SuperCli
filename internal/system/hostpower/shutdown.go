package hostpower

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

// shutdownPlan is deliberately separate from execution so tests inspect only
// argv. No force, privilege escalation, shell, or reboot is ever requested.
func shutdownPlan(goos string) (string, []string, error) {
	switch goos {
	case "windows":
		// A positive Windows timeout implicitly forces applications closed.
		// Our countdown already elapsed; /t 0 without /f is intentional.
		return "shutdown.exe", []string{"/s", "/t", "0"}, nil
	case "linux", "darwin":
		return "/sbin/shutdown", []string{"-h", "now"}, nil
	default:
		return "", nil, fmt.Errorf("host shutdown is unsupported on %s", goos)
	}
}

// Shutdown is the live production adapter. It is never selected implicitly by
// New. Applications must explicitly wire it after implementing trusted consent
// and verified-work integration. Tests must inspect shutdownPlan, not call this.
func Shutdown(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name, args, err := shutdownPlan(runtime.GOOS)
	if err != nil {
		return err
	}
	if output, err := exec.CommandContext(ctx, name, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("host shutdown failed: %w: %s", err, output)
	}
	return nil
}
