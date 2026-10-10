//go:build !unix

package ctxexec

import (
	"os/exec"

	"supercli/internal/system/childproc"
)

func configureCommandScope(_ *exec.Cmd) {}

// Windows uses the owned Job Object; unsupported jobs fall back to the parent.
func killCommandTree(cmd *exec.Cmd, scope *childproc.Scope) error {
	return scope.Kill(cmd)
}
