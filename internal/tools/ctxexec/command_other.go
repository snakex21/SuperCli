//go:build !windows

package ctxexec

import "os/exec"

func configureCommandLine(_ *exec.Cmd, _ []string) {}

const cmdMultilineMessage = ""

func cmdScriptDropsLines(_ string, _ []string) bool { return false }
