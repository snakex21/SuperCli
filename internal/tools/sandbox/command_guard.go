package sandbox

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Destructive command guard (1.0.39). File tools are confined to the project
// and their deletes go to the trash, but a command ran with no check at all:
// one call could wipe a disk, the user's home, the project or its history.
// Every tool that runs commands (ctx_execute, process_session start/write)
// classifies the command before it runs:
//
//   - Block: certain mass or irreversible damage (disk/partition/power
//     commands, raw device writes, deletes of a root, home, system folder,
//     anything outside the project, the project itself or its .git, git
//     reset --hard / force push / clean -fdx). The model is told to ask the
//     user to run it.
//   - Ask: a delete whose target cannot be known before it runs (a shell
//     variable, an encoded command, targets from stdin). The user approves
//     or denies; nothing runs silently.
//   - Allow: everything else, including builds, tests and deletes of
//     specific files and folders inside the project.
//
// Commands are parsed with their quoting (POSIX sh, PowerShell, cmd), so a
// separator inside a quoted argument ("fix bug & reboot fixture") is text,
// not a second command; wrappers (bash -c, cmd /c, powershell -Command,
// sudo, env) are unwrapped to the program that really runs.

// CommandVerdict is the guard's decision.
type CommandVerdict int

const (
	CommandAllow CommandVerdict = iota
	CommandAsk
	CommandBlock
)

// CommandCheck is the verdict with the reason shown to the model and user.
type CommandCheck struct {
	Verdict CommandVerdict
	Reason  string
}

type shellKind int

const (
	shellArgv shellKind = iota // exec'd directly: arguments are literal
	shellPOSIX
	shellPwsh
	shellCmd
)

// CheckCommand classifies argv run with workdir (relative to project) inside
// the project root base ("" when unknown).
func CheckCommand(argv []string, base, workdir string) CommandCheck {
	g := guard{base: cleanBase(base), workdir: workdir}
	g.check(argv, shellArgv, 0)
	return g.result
}

// CheckShellText classifies text typed into an interactive shell (any shell
// syntax that could apply).
func CheckShellText(text, base, workdir string) CommandCheck {
	worst := CommandCheck{}
	for _, kind := range []shellKind{shellPOSIX, shellPwsh, shellCmd} {
		g := guard{base: cleanBase(base), workdir: workdir}
		for _, seg := range splitShell(text, kind) {
			g.segment(seg, kind, 1)
		}
		if g.result.Verdict > worst.Verdict {
			worst = g.result
		}
	}
	return worst
}

type guard struct {
	base, workdir string
	result        CommandCheck
}

func (g *guard) raise(v CommandVerdict, reason string) {
	if v > g.result.Verdict {
		g.result = CommandCheck{Verdict: v, Reason: reason}
	}
}

func cleanBase(base string) string {
	if strings.TrimSpace(base) == "" {
		return ""
	}
	if abs, err := filepath.Abs(base); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(base)
}

func (g *guard) check(argv []string, kind shellKind, depth int) {
	if len(argv) == 0 || depth > 4 {
		return
	}
	g.segment(argv, kind, depth)
}

var (
	destructiveProgram = regexp.MustCompile(`^(format|format\.com|diskpart|mkfs(\.\w+)?|shutdown|reboot|halt|poweroff|stop-computer|restart-computer|clear-disk|initialize-disk|remove-partition|format-volume|fdisk|wipefs)$`)
	deviceTarget       = regexp.MustCompile(`(?i)^of=(/dev/|\\\\\.\\)`)
	shellVariable      = regexp.MustCompile(`\$\{?[A-Za-z_][A-Za-z0-9_:]*\}?|\$\(|` + "`" + `|%[A-Za-z_][A-Za-z0-9_]*%|\$env:`)
)

func programName(word string) string {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(word, `\`, "/")))
	for _, ext := range []string{".exe", ".com", ".cmd", ".bat"} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}

// segment classifies one command (program + arguments) of the given syntax.
func (g *guard) segment(seg []string, kind shellKind, depth int) {
	seg = stripPrefixWrappers(seg)
	if len(seg) == 0 {
		return
	}
	name := programName(seg[0])
	args := seg[1:]
	// Shell wrappers: classify the command text they run.
	if text, inner, ok := wrappedCommand(name, args); ok {
		if inner == shellUnknown {
			g.raise(CommandAsk, "an encoded or indirect shell command whose content cannot be checked")
			return
		}
		for _, sub := range splitShell(text, inner) {
			g.segment(sub, inner, depth+1)
		}
		return
	}
	switch {
	case destructiveProgram.MatchString(name):
		g.raise(CommandBlock, "disk, partition or power command: "+name)
	case name == "dd":
		for _, a := range args {
			if deviceTarget.MatchString(a) {
				g.raise(CommandBlock, "raw write to a device")
			}
		}
	case name == "git":
		g.git(args)
	case name == "find":
		g.find(args)
	case name == "xargs":
		if len(args) > 0 && isDeleteProgram(programName(firstNonFlag(args)), kind) {
			g.raise(CommandAsk, "a delete whose targets come from another command's output")
		}
	default:
		if isDeleteProgram(name, kind) {
			g.delete(name, args, kind)
		}
	}
}

const shellUnknown shellKind = -1

// wrappedCommand returns the command text a shell wrapper runs.
func wrappedCommand(name string, args []string) (string, shellKind, bool) {
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "busybox":
		for i, a := range args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") && i+1 < len(args) {
				return args[i+1], shellPOSIX, true
			}
		}
	case "cmd":
		for i, a := range args {
			if l := strings.ToLower(a); l == "/c" || l == "/k" {
				return strings.Join(args[i+1:], " "), shellCmd, true
			}
		}
	case "powershell", "pwsh":
		for i := 0; i < len(args); i++ {
			l := strings.ToLower(args[i])
			if l == "-e" || strings.HasPrefix(l, "-enc") || strings.HasPrefix(l, "-encodedcommand") {
				return "", shellUnknown, true
			}
			if l == "-c" || l == "-command" || (strings.HasPrefix("-command", l) && len(l) >= 4) {
				return strings.Join(args[i+1:], " "), shellPwsh, true
			}
			if l == "-file" || l == "-f" {
				return "", shellArgv, false // a script file: not inspected here
			}
			if !strings.HasPrefix(l, "-") {
				// powershell "Remove-Item ..." (implicit -Command)
				return strings.Join(args[i:], " "), shellPwsh, true
			}
			if l == "-executionpolicy" || l == "-ep" || l == "-windowstyle" || l == "-workingdirectory" {
				i++ // option value
			}
		}
	}
	return "", shellArgv, false
}

// stripPrefixWrappers drops programs that only run the next one (sudo, env
// with VAR=value pairs, nohup, time, nice, exec, command, call, start).
func stripPrefixWrappers(seg []string) []string {
	for len(seg) > 0 {
		switch programName(seg[0]) {
		case "sudo", "doas", "nohup", "time", "exec", "command", "call", "builtin", "nice", "ionice", "&":
			seg = seg[1:]
			for len(seg) > 0 && strings.HasPrefix(seg[0], "-") {
				seg = seg[1:]
			}
			continue
		case "env":
			seg = seg[1:]
			for len(seg) > 0 && (strings.Contains(seg[0], "=") || strings.HasPrefix(seg[0], "-")) {
				seg = seg[1:]
			}
			continue
		}
		return seg
	}
	return seg
}

func firstNonFlag(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// isDeleteProgram: rm/unlink (POSIX) and Remove-Item with its PowerShell
// aliases; del/erase/rd/rmdir in cmd.
func isDeleteProgram(name string, kind shellKind) bool {
	switch name {
	case "rm", "unlink", "rmdir", "shred", "srm":
		return true
	case "remove-item", "ri", "del", "erase", "rd", "rp":
		return name != "rp" || kind == shellPwsh
	}
	return false
}

// delete checks every target of a delete command.
func (g *guard) delete(name string, args []string, kind shellKind) {
	pwsh := kind == shellPwsh || name == "remove-item" || name == "ri"
	cmd := kind == shellCmd && (name == "del" || name == "erase" || name == "rd" || name == "rmdir")
	var targets []string
	endFlags := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		l := strings.ToLower(a)
		switch {
		case endFlags:
			targets = append(targets, a)
		case a == "--" && !pwsh && !cmd:
			endFlags = true
		case pwsh && strings.HasPrefix(a, "-"):
			// PowerShell parameters are case-insensitive prefixes; these take
			// a value. -Path/-LiteralPath values are targets.
			p := strings.TrimPrefix(l, "-")
			if p != "" && (strings.HasPrefix("path", p) || strings.HasPrefix("literalpath", p) && len(p) >= 2) && i+1 < len(args) {
				targets = append(targets, args[i+1])
				i++
			} else if p != "" && (strings.HasPrefix("include", p) || strings.HasPrefix("exclude", p) || strings.HasPrefix("filter", p) || strings.HasPrefix("stream", p)) && i+1 < len(args) {
				i++
			}
		case cmd && strings.HasPrefix(a, "/"):
			// /s /q /f /a — flags; a bare "/" would be a path but cmd has none
		case !pwsh && !cmd && strings.HasPrefix(a, "-") && a != "-":
			// rm flags
		default:
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 && name == "xargs" {
		g.raise(CommandAsk, "a delete whose targets come from another command's output")
	}
	for _, t := range targets {
		g.target(t, "delete")
	}
}

// target classifies one path a destructive command acts on.
func (g *guard) target(raw, what string) {
	t := strings.Trim(raw, `"'`)
	if t == "" {
		return
	}
	if shellVariable.MatchString(t) {
		g.raise(CommandAsk, what+" of a path that depends on a variable ("+t+")")
		return
	}
	home, _ := os.UserHomeDir()
	if t == "~" || strings.HasPrefix(t, "~/") || strings.HasPrefix(t, `~\`) {
		if home == "" {
			g.raise(CommandAsk, what+" of a path in the home folder ("+t+")")
			return
		}
		t = filepath.Join(home, t[1:])
	}
	// A glob deletes what its folder contains: judge that folder, and the
	// folder itself when the glob is everything in it.
	dir, container := t, false
	if i := strings.IndexAny(t, "*?["); i >= 0 {
		// "build/*.o" deletes some files in build; "*" (everything) deletes
		// the folder's whole content, judged like the folder itself.
		pattern := t[i:]
		if j := strings.LastIndexAny(t[:i], "/\\"); j >= 0 {
			pattern = t[j+1:]
		} else {
			pattern = t
		}
		container = pattern != "*" && pattern != "*.*" && pattern != ".*" && pattern != "**"
		dir = t[:i]
		if j := strings.LastIndexAny(dir, `/\`); j >= 0 {
			dir = dir[:j+1]
		} else {
			dir = "."
		}
	}
	abs := g.resolve(dir)
	if abs == "" {
		g.raise(CommandAsk, what+" of a path that cannot be resolved ("+raw+")")
		return
	}
	if why := g.protected(abs, home, container); why != "" {
		g.raise(CommandBlock, what+" of "+why+" ("+raw+")")
	}
}

// resolve turns a target into a clean absolute path (relative to the
// project and workdir). "" when there is no base to resolve against.
func (g *guard) resolve(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		p = "."
	}
	if runtime.GOOS == "windows" {
		p = strings.ReplaceAll(p, "/", `\`)
	}
	switch {
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	case runtime.GOOS == "windows" && (strings.HasPrefix(p, `\`)):
		vol := filepath.VolumeName(g.base)
		if vol == "" {
			vol = "C:"
		}
		return filepath.Clean(vol + p)
	case runtime.GOOS == "windows" && len(p) == 2 && p[1] == ':':
		return filepath.Clean(p + `\`)
	case g.base == "":
		return ""
	}
	return filepath.Clean(filepath.Join(g.base, g.workdir, p))
}

// protected names why abs must not be destroyed by a command, or "".
func (g *guard) protected(abs, home string, container bool) string {
	if isRoot(abs) {
		return "a drive or filesystem root"
	}
	if home != "" && (samePath(abs, home) || within(home, abs)) {
		return "the home folder or one of its parents"
	}
	for _, sys := range systemDirs() {
		if samePath(abs, sys) || within(abs, sys) || within(sys, abs) {
			return "a system folder"
		}
	}
	for _, users := range userRoots() {
		if samePath(abs, users) || within(users, abs) || samePath(filepath.Dir(abs), users) {
			return "a user's home folder"
		}
	}
	if g.base == "" {
		return ""
	}
	if within(g.base, abs) || samePath(abs, g.base) && !container {
		return "the whole project"
	}
	if !within(abs, g.base) && !samePath(abs, g.base) {
		return "a path outside the project"
	}
	rel, err := filepath.Rel(g.base, abs)
	if err == nil {
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if strings.EqualFold(part, ".git") {
				return "the project's git history (.git)"
			}
		}
	}
	return ""
}

func isRoot(p string) bool {
	p = filepath.Clean(p)
	return p == string(filepath.Separator) || p == filepath.VolumeName(p)+string(filepath.Separator) || p == filepath.VolumeName(p)
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// within reports whether p is strictly inside dir.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// systemDirs hold the operating system and installed programs: nothing in
// them is a project file.
func systemDirs() []string {
	if runtime.GOOS == "windows" {
		var out []string
		for _, env := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
			if v := os.Getenv(env); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	return []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/sbin", "/sys", "/usr", "/System", "/Library", "/Applications"}
}

// userRoots hold the users' home folders: the root itself and each home
// directly in it are protected; projects deeper inside are not.
func userRoots() []string {
	if runtime.GOOS == "windows" {
		drive := os.Getenv("SystemDrive")
		if drive == "" {
			drive = "C:"
		}
		return []string{drive + `\Users`}
	}
	return []string{"/home", "/Users", "/root"}
}

// git: global options, then the subcommand's dangerous forms.
func (g *guard) git(args []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace" || a == "--exec-path" || a == "--config-env":
			i += 2
			continue
		case strings.HasPrefix(a, "-"):
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return
	}
	sub, opts := strings.ToLower(args[i]), args[i+1:]
	switch sub {
	case "push":
		for _, o := range opts {
			l := strings.ToLower(o)
			if l == "-f" || l == "--force" || strings.HasPrefix(l, "--force-with-lease") || l == "--mirror" || l == "--delete" || l == "-d" || strings.HasPrefix(o, "+") || strings.HasPrefix(o, ":") {
				g.raise(CommandBlock, "git force push or remote delete")
			}
		}
	case "reset":
		for _, o := range opts {
			if strings.EqualFold(o, "--hard") || strings.EqualFold(o, "--merge") {
				g.raise(CommandBlock, "git reset --hard discards uncommitted work")
			}
		}
	case "clean":
		for _, o := range opts {
			l := strings.ToLower(o)
			if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "--") && strings.Contains(l, "f") || l == "--force" {
				g.raise(CommandBlock, "git clean deletes untracked files")
			}
		}
	case "checkout", "restore":
		for _, o := range opts {
			if o == "." || o == "*" {
				g.raise(CommandBlock, "git "+sub+" of the whole tree discards uncommitted work")
			}
		}
	}
}

// find with -delete or -exec rm deletes what it finds under its start paths.
func (g *guard) find(args []string) {
	var paths []string
	deletes := false
	expr := false
	for i, a := range args {
		if !expr && (strings.HasPrefix(a, "-") || a == "(" || a == "!" || a == `\(`) {
			expr = true
		}
		if !expr {
			paths = append(paths, a)
			continue
		}
		if a == "-delete" {
			deletes = true
		}
		if (a == "-exec" || a == "-execdir" || a == "-ok") && i+1 < len(args) && isDeleteProgram(programName(args[i+1]), shellPOSIX) {
			deletes = true
		}
	}
	if !deletes {
		return
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	for _, p := range paths {
		g.target(p, "find -delete")
	}
}

// splitShell splits command text into commands (word lists) with the quoting
// rules of the given shell. Separators inside quotes are text.
func splitShell(text string, kind shellKind) [][]string {
	var segs [][]string
	var words []string
	var cur strings.Builder
	inWord := false
	flushWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	flushSeg := func() {
		flushWord()
		if len(words) > 0 {
			segs = append(segs, words)
		}
		words = nil
	}
	r := []rune(text)
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\'' && kind != shellCmd:
			inWord = true
			for i++; i < len(r); i++ {
				if r[i] == '\'' {
					if kind == shellPwsh && i+1 < len(r) && r[i+1] == '\'' {
						cur.WriteRune('\'')
						i++
						continue
					}
					break
				}
				cur.WriteRune(r[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(r); i++ {
				if r[i] == '"' {
					break
				}
				if kind == shellPOSIX && r[i] == '\\' && i+1 < len(r) && strings.ContainsRune("\"\\$`\n", r[i+1]) {
					i++
				} else if kind == shellPwsh && r[i] == '`' && i+1 < len(r) {
					i++
				}
				cur.WriteRune(r[i])
			}
		case kind == shellPOSIX && c == '\\' && i+1 < len(r):
			inWord = true
			i++
			cur.WriteRune(r[i])
		case kind == shellPwsh && c == '`' && i+1 < len(r):
			inWord = true
			i++
			cur.WriteRune(r[i])
		case kind == shellCmd && c == '^' && i+1 < len(r):
			inWord = true
			i++
			cur.WriteRune(r[i])
		case c == ';' && kind != shellCmd, c == '&', c == '|', c == '\n', c == '\r':
			flushSeg()
		case c == '(' && kind != shellCmd && !inWord, c == ')' && kind != shellCmd:
			flushSeg()
		case c == ' ' || c == '\t':
			flushWord()
		case c == '#' && kind == shellPOSIX && !inWord:
			for i < len(r) && r[i] != '\n' {
				i++
			}
			flushSeg()
		default:
			inWord = true
			cur.WriteRune(c)
		}
	}
	flushSeg()
	return segs
}
