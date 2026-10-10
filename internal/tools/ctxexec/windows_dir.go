package ctxexec

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// WindowsDirCommand is a validated native builtin listing. Its executable and
// script stay private so a checkpoint bypass cannot bind an arbitrary command.
type WindowsDirCommand struct {
	original, envExtra, effective []string
}

// ResolveWindowsDirCommand recognizes only literal dir listings, including the
// bare builtin when no executable named dir resolves. Callers must restrict this
// Windows mapping to Windows; explicit resolver inputs also permit portable tests.
func ResolveWindowsDirCommand(command, envExtra []string, systemRoot string, lookPath func(string) (string, error)) (WindowsDirCommand, bool) {
	if len(command) == 0 || len(envExtra) != 0 || !filepath.IsAbs(systemRoot) || lookPath == nil {
		return WindowsDirCommand{}, false
	}
	var script, cmdName string
	if strings.EqualFold(command[0], "dir") {
		var ok bool
		script, ok = bareWindowsDirScript(command[1:])
		if !ok {
			return WindowsDirCommand{}, false
		}
		// ErrDot and all other lookup failures remain conservative. A real
		// project/PATH executable must keep argv semantics and its checkpoint.
		if _, err := lookPath(command[0]); !errors.Is(err, exec.ErrNotFound) {
			return WindowsDirCommand{}, false
		}
		cmdName = "cmd.exe"
	} else if strings.EqualFold(command[0], "cmd") || strings.EqualFold(command[0], "cmd.exe") {
		var ok bool
		script, ok = explicitWindowsDirScript(command)
		if !ok {
			return WindowsDirCommand{}, false
		}
		cmdName = command[0]
	} else {
		return WindowsDirCommand{}, false
	}
	resolved, err := lookPath(cmdName)
	native := filepath.Join(systemRoot, "System32", "cmd.exe")
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), native) {
		return WindowsDirCommand{}, false
	}
	return WindowsDirCommand{
		original:  append([]string(nil), command...),
		envExtra:  append([]string(nil), envExtra...),
		effective: []string{native, "/d", "/s", "/c", script},
	}, true
}

type windowsDirContextKey struct{}
type boundWindowsDir struct {
	command WindowsDirCommand
	workdir string
}

// WithWindowsDirCommand binds admission to execution. A different command,
// environment or workdir in a nested invocation cannot consume this mapping.
func WithWindowsDirCommand(ctx context.Context, command WindowsDirCommand, workdir string) context.Context {
	if len(command.effective) == 0 {
		return ctx
	}
	return context.WithValue(ctx, windowsDirContextKey{}, boundWindowsDir{command: command, workdir: workdir})
}

func boundWindowsDirCommand(ctx context.Context, req *Request) ([]string, bool) {
	bound, ok := ctx.Value(windowsDirContextKey{}).(boundWindowsDir)
	if !ok || bound.workdir != req.Workdir || !sameCommandStrings(bound.command.original, req.Command) || !sameCommandStrings(bound.command.envExtra, req.EnvExtra) {
		return nil, false
	}
	return bound.command.effective, true
}

func sameCommandStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func bareWindowsDirScript(args []string) (string, bool) {
	parts := []string{"dir"}
	paths := 0
	for _, arg := range args {
		if arg == "" || strings.ContainsRune(arg, '"') || unsafeWindowsDirText(arg, false) {
			return "", false
		}
		if strings.EqualFold(arg, "/b") || strings.EqualFold(arg, "/a") {
			parts = append(parts, arg)
			continue
		}
		if strings.HasPrefix(arg, "/") {
			return "", false
		}
		paths++
		if paths > 1 {
			return "", false
		}
		parts = append(parts, "\""+arg+"\"")
	}
	return strings.Join(parts, " "), true
}

func explicitWindowsDirScript(command []string) (string, bool) {
	index := 1
	seenD, seenS := false, false
	for index < len(command) && !strings.EqualFold(command[index], "/c") {
		switch strings.ToLower(command[index]) {
		case "/d":
			if seenD {
				return "", false
			}
			seenD = true
		case "/s":
			if seenS {
				return "", false
			}
			seenS = true
		default:
			return "", false
		}
		index++
	}
	if index+1 >= len(command) {
		return "", false
	}
	parts := command[index+1:]
	script := parts[0]
	if len(parts) > 1 {
		// Match configureCommandLine's assembly of an explicit cmd script.
		quoted := make([]string, len(parts))
		for i, part := range parts {
			if strings.ContainsAny(part, " \t") && !strings.ContainsRune(part, '"') {
				part = "\"" + part + "\""
			}
			quoted[i] = part
		}
		script = strings.Join(quoted, " ")
	}
	return script, literalWindowsDirScript(script)
}

func unsafeWindowsDirText(text string, allowTab bool) bool {
	if strings.ContainsAny(text, "&|<>^%!()`$") {
		return true
	}
	for _, ch := range text {
		if unicode.IsControl(ch) && !(allowTab && ch == '\t') {
			return true
		}
	}
	return false
}

func literalWindowsDirScript(script string) bool {
	if unsafeWindowsDirText(script, true) {
		return false
	}
	script = strings.Trim(script, " \t")
	if len(script) < 3 || !strings.EqualFold(script[:3], "dir") || (len(script) > 3 && script[3] != ' ' && script[3] != '\t') {
		return false
	}
	remaining := script[3:]
	paths := 0
	for {
		remaining = strings.TrimLeft(remaining, " \t")
		if remaining == "" {
			return true
		}
		var token string
		if remaining[0] == '"' {
			end := strings.IndexByte(remaining[1:], '"')
			if end < 0 {
				return false
			}
			token = remaining[1 : end+1]
			remaining = remaining[end+2:]
			if remaining != "" && remaining[0] != ' ' && remaining[0] != '\t' {
				return false
			}
		} else {
			end := strings.IndexAny(remaining, " \t")
			if end < 0 {
				end = len(remaining)
			}
			token, remaining = remaining[:end], remaining[end:]
			if strings.ContainsRune(token, '"') {
				return false
			}
		}
		if token == "" {
			return false
		}
		if strings.EqualFold(token, "/b") || strings.EqualFold(token, "/a") {
			continue
		}
		if strings.HasPrefix(token, "/") {
			return false
		}
		paths++
		if paths > 1 {
			return false
		}
	}
}
