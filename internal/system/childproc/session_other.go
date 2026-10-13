//go:build !unix && !windows

package childproc

import "os/exec"

func detachSession(*exec.Cmd) {}
