//go:build !windows

package ctxexec

import "os/exec"

func configureCommandLine(_ *exec.Cmd, _ []string) {}
