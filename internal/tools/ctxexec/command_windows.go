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

// cmdMultilineMessage explains why a multi-line cmd script was refused.
const cmdMultilineMessage = "cmd.exe runs only the first line of a /c script and silently drops the rest, so the command was not run. " +
	"Write the script to a file with create_file and run it (e.g. [\"python\",\"script.py\"]), or call the interpreter " +
	"directly without cmd (e.g. [\"python\",\"-c\",\"...\"] or [\"powershell\",\"-Command\",\"...\"]), which keep newlines."

// cmdScriptDropsLines reports an explicit cmd /c or /k script spanning several
// lines. cmd.exe stops parsing its command line at the first line break, so
// everything after it would vanish while the command still exits 0; the model
// then sees empty output and retries the same script over and over. A single
// trailing line break is harmless and allowed.
func cmdScriptDropsLines(binary string, args []string) bool {
	name := strings.ToLower(filepath.Base(binary))
	if name != "cmd.exe" && name != "cmd" {
		return false
	}
	for i, arg := range args {
		if !strings.EqualFold(arg, "/c") && !strings.EqualFold(arg, "/k") {
			continue
		}
		script := strings.TrimRight(strings.Join(args[i+1:], " "), "\r\n")
		return strings.ContainsAny(script, "\r\n")
	}
	return false
}
