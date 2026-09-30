package agent

import (
	"path/filepath"
	"strings"
)

// Recognize only a literal single command inside known shell wrappers. This is
// classification, never shell evaluation or argument rewriting. Failed-check
// keys keep the original argv and environment, so a different invocation cannot
// clear the failure. Quoting, expansion, pipelines and scripts remain opaque.
func unwrapVerificationShell(command []string) []string {
	for depth := 0; depth < 4 && len(command) > 0; depth++ {
		exe := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(command[0], "\\", "/"))), ".exe")
		var mode string
		switch exe {
		case "cmd":
			mode = "cmd"
		case "powershell", "pwsh":
			mode = "powershell"
		case "sh", "bash", "dash", "zsh":
			mode = "unix"
		default:
			return command
		}
		body := shellVerificationBody(command[1:], mode)
		if body == "" || strings.ContainsAny(body, "|&;<>$`%(){}[]\"'\r\n\x00") {
			return nil
		}
		command = strings.Fields(body)
	}
	return nil // Bound nested wrappers instead of recursively parsing arbitrary text.
}

func shellVerificationBody(args []string, mode string) string {
	for i, arg := range args {
		arg = strings.ToLower(arg)
		end := false
		switch mode {
		case "cmd":
			switch arg {
			case "/c":
				end = true
			case "/d", "/s", "/q":
			default:
				return ""
			}
		case "powershell":
			switch arg {
			case "-command", "-c":
				end = true
			case "-noprofile", "-noninteractive", "-nologo":
			default:
				return ""
			}
		case "unix":
			switch arg {
			case "-c", "-lc", "-ec":
				end = true
			case "-l", "-e", "-u":
			default:
				return ""
			}
		}
		if end {
			body := args[i+1:]
			if len(body) == 1 {
				return body[0]
			}
			// Unix/PowerShell arguments after the script may be positional parameters,
			// not extra command text. CMD joins the remaining argv into one command.
			if mode == "cmd" {
				return strings.Join(body, " ")
			}
			return ""
		}
	}
	return ""
}
