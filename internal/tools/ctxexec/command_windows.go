//go:build windows

package ctxexec

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// cmd.exe does not use the CommandLineToArgvW escaping used by os/exec.
// In particular \" reaches cmd as a literal backslash and breaks scripts.
// Only an explicitly requested cmd /c or /k uses this path; other binaries
// keep argv semantics, including PowerShell and paths containing spaces.
func configureCommandLine(cmd *exec.Cmd, args []string) {
	name := strings.ToLower(filepath.Base(cmd.Path))
	if name != "cmd.exe" && name != "cmd" {
		return
	}
	for i, arg := range args {
		if !strings.EqualFold(arg, "/c") && !strings.EqualFold(arg, "/k") {
			continue
		}
		if i+1 >= len(args) {
			return
		}
		tail := args[i+1:]
		script := tail[0]
		if len(tail) > 1 {
			parts := make([]string, len(tail))
			for j, part := range tail {
				if strings.ContainsAny(part, " \t") && !strings.ContainsRune(part, '"') {
					part = "\"" + part + "\""
				}
				parts[j] = part
			}
			script = strings.Join(parts, " ")
		}
		if cmd.SysProcAttr == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
		}
		// /s strips exactly this outer pair, leaving inner script quotes intact.
		flags := strings.Join(args[:i], " ")
		cmd.SysProcAttr.CmdLine = "\"" + cmd.Path + "\" /d /s " + flags + " " + arg + " \"" + script + "\""
		return
	}
}
