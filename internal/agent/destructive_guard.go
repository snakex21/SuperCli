package agent

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// Destructive command guard (1.0.39). File tools are confined to the project
// and deletes go to the trash, but a shell command ran with no check at all:
// a model could wipe a disk, the user's home or the whole project, or throw
// away uncommitted work, with one ctx_execute call. These commands are refused
// before they run; the model is told to ask the user to run them. The list is
// deliberately short and specific (mass or irreversible damage), so ordinary
// builds, tests and single-file deletes inside the project are unaffected.

var (
	destructiveProgram = regexp.MustCompile(`(?i)^(format|format\.com|diskpart|mkfs(\.\w+)?|shutdown|reboot|halt|poweroff|stop-computer|restart-computer|clear-disk|initialize-disk|remove-partition)$`)
	destructiveDD      = regexp.MustCompile(`(?i)\bof=(/dev/|\\\\\.\\)`)
	gitForcePush       = regexp.MustCompile(`(?i)^(-f|--force|--force-with-lease|--mirror|--delete|-d)$`)
)

// destructiveCommandRefusal returns a refusal for a catastrophic command, or "".
func destructiveCommandRefusal(toolName, rawArgs string) string {
	if toolName != "ctx_execute" {
		return ""
	}
	var args struct {
		Command []string `json:"command"`
	}
	if json.Unmarshal([]byte(rawArgs), &args) != nil || len(args.Command) == 0 {
		return ""
	}
	if why := destructiveReason(args.Command); why != "" {
		return "blocked: destructive command (" + why + "). It was not run. " +
			"Never run it yourself: tell the user exactly what it would do and ask them to run it if they want it."
	}
	return ""
}

func destructiveReason(argv []string) string {
	for _, seg := range commandSegments(argv) {
		if why := destructiveSegment(seg); why != "" {
			return why
		}
	}
	return ""
}

func programName(word string) string {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(word, `\`, "/")))
	return strings.TrimSuffix(name, ".exe")
}

func destructiveSegment(seg []string) string {
	if len(seg) == 0 {
		return ""
	}
	name := programName(seg[0])
	rest := seg[1:]
	switch {
	case destructiveProgram.MatchString(name):
		return "disk, partition or power command: " + name
	case name == "dd" && destructiveDD.MatchString(strings.Join(rest, " ")):
		return "raw write to a device"
	case name == "rm" || name == "remove-item" || name == "ri" || name == "del" || name == "erase" || name == "rd" || name == "rmdir":
		if recursive(name, rest) && anyBroadTarget(rest) {
			return "recursive delete of a root, home, parent or wildcard path"
		}
	case name == "git" && len(rest) > 0:
		sub := strings.ToLower(rest[0])
		opts := rest[1:]
		switch sub {
		case "push":
			for _, o := range opts {
				if gitForcePush.MatchString(o) || strings.HasPrefix(o, "+") {
					return "git force push or remote delete"
				}
			}
		case "reset":
			for _, o := range opts {
				if strings.EqualFold(o, "--hard") {
					return "git reset --hard discards uncommitted work"
				}
			}
		case "clean":
			for _, o := range opts {
				l := strings.ToLower(o)
				if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "--") && strings.Contains(l, "f") && strings.ContainsAny(l, "dx") {
					return "git clean deletes untracked files"
				}
			}
		}
	}
	return ""
}

// shellWrappers run their argument as a command line ("cmd /c ...",
// "powershell -Command ...", "bash -c ..."); the real program follows them.
var shellWrappers = map[string]bool{"cmd": true, "bash": true, "sh": true, "zsh": true, "powershell": true, "pwsh": true, "sudo": true, "env": true, "nohup": true, "call": true}

// commandSegments splits argv, including shell text carried inside it, into
// commands at ; & | and newlines, and strips wrappers and their options, so
// each segment starts with the program that really runs. A word counts as a
// program only in that position ("git commit -m 'format code'" runs git).
func commandSegments(argv []string) [][]string {
	var segs [][]string
	cur := []string{}
	flush := func() {
		if len(cur) > 0 {
			segs = append(segs, cur)
		}
		cur = []string{}
	}
	separator := func(r rune) bool { return r == ';' || r == '&' || r == '|' || r == '\n' }
	for _, a := range argv {
		start := 0
		for i, r := range a {
			if separator(r) {
				cur = appendWords(cur, a[start:i])
				flush()
				start = i + 1
			}
		}
		cur = appendWords(cur, a[start:])
	}
	flush()
	for i, seg := range segs {
		for len(seg) > 0 {
			head := programName(seg[0])
			lower := strings.ToLower(seg[0])
			if shellWrappers[head] || strings.HasPrefix(lower, "-") || strings.HasPrefix(lower, "/") && len(lower) <= 2 {
				seg = seg[1:]
				continue
			}
			if lower == "bypass" || lower == "unrestricted" || lower == "remotesigned" {
				seg = seg[1:] // -ExecutionPolicy value
				continue
			}
			break
		}
		segs[i] = seg
	}
	return segs
}

func appendWords(dst []string, text string) []string {
	for _, f := range strings.Fields(text) {
		if f = strings.Trim(f, `"'`); f != "" {
			dst = append(dst, f)
		}
	}
	return dst
}

func recursive(name string, rest []string) bool {
	for _, o := range rest {
		l := strings.ToLower(o)
		switch {
		case name == "rm" && strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "--") && strings.ContainsAny(o, "rR"):
			return true
		case l == "--recursive" || l == "-recurse" || l == "/s":
			return true
		}
	}
	return false
}

// anyBroadTarget: the delete names a drive or filesystem root, the home
// directory, a parent directory, or a bare wildcard (everything here).
func anyBroadTarget(rest []string) bool {
	for _, o := range rest {
		p := strings.Trim(o, `"'`)
		if p == "" || strings.HasPrefix(p, "-") || (strings.HasPrefix(p, "/") && len(p) == 2) {
			continue // flags, including /s /q
		}
		l := strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
		l = strings.TrimSuffix(l, "/*")
		l = strings.TrimSuffix(l, "/")
		switch {
		case l == "" || l == "*" || l == "." || l == ".." || l == "~" || l == "$home" || l == "%userprofile%" || l == "$env:userprofile":
			return true
		case len(l) == 2 && l[1] == ':': // C:, D:
			return true
		case strings.HasPrefix(l, "../"):
			return true
		}
	}
	return false
}
