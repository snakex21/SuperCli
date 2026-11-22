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
// classifies the command before it runs. The rule is "ask by default" for
// anything destructive (a blacklist is always full of holes):
//
//   - Allow: commands that delete or destroy nothing, and deletes whose every
//     target is a literal path inside the project (after resolving cd,
//     pushd, Set-Location, "..", "~") - not the project itself, not .git.
//   - Block: unambiguous disasters - a drive or filesystem root, a home
//     folder or its parents, a user's home, a system folder, the project or
//     its .git; disk/partition/power commands, raw device writes; git reset
//     --hard, force push, clean -f.
//   - Ask (the user approves or denies; nothing runs silently): everything
//     else that deletes - targets outside the project, targets from a pipe
//     or a substitution ($(...), (...), backticks), variables, loops,
//     encoded or dynamic commands (-EncodedCommand and every -e prefix, iex,
//     Invoke-Expression, eval), deletes behind wrappers (sudo, busybox, env,
//     nohup, xargs), an unknown working directory.
//
// Commands are parsed with their quoting (POSIX sh, PowerShell, cmd), so a
// separator inside a quoted argument ("fix bug & reboot fixture") is text.

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
	shellUnknown shellKind = -1
)

// CheckCommand classifies argv run in workdir (relative to the project root
// base; "" when unknown).
func CheckCommand(argv []string, base, workdir string) CommandCheck {
	g := newGuard(base, workdir)
	g.run([]shellSeg{{words: argv}}, shellArgv, 0)
	return g.result
}

// CheckShellText classifies text typed into an interactive shell (whichever
// shell syntax could apply; the strictest verdict wins).
func CheckShellText(text, base, workdir string) CommandCheck {
	worst := CommandCheck{}
	for _, kind := range []shellKind{shellPOSIX, shellPwsh, shellCmd} {
		g := newGuard(base, workdir)
		g.run(splitShell(text, kind), kind, 1)
		if g.result.Verdict > worst.Verdict {
			worst = g.result
		}
	}
	return worst
}

type guard struct {
	base     string // project root, absolute ("" unknown)
	cwd      string // current directory of the command, absolute ("" unknown)
	inLoop   bool
	wrapped  string // the wrapper a delete hides behind (sudo, busybox, ...)
	result   CommandCheck
	homeDir  string
	homeRead bool
}

func newGuard(base, workdir string) *guard {
	g := &guard{base: cleanBase(base)}
	if g.base != "" {
		g.cwd = filepath.Clean(filepath.Join(g.base, workdir))
		if filepath.IsAbs(workdir) {
			g.cwd = filepath.Clean(workdir)
		}
	}
	return g
}

func (g *guard) home() string {
	if !g.homeRead {
		g.homeDir, _ = os.UserHomeDir()
		g.homeRead = true
	}
	return g.homeDir
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

var (
	destructiveProgram = regexp.MustCompile(`^(format|format\.com|diskpart|mkfs(\.\w+)?|shutdown|reboot|halt|poweroff|stop-computer|restart-computer|clear-disk|initialize-disk|remove-partition|format-volume|fdisk|wipefs|cipher)$`)
	deviceTarget       = regexp.MustCompile(`(?i)^of=(/dev/|\\\\\.\\)`)
	// A value only known when the command runs.
	dynamicValue = regexp.MustCompile(`\$|` + "`" + `|%[A-Za-z_][A-Za-z0-9_]*%|%%?[A-Za-z]\b|^\(|^\{`)
)

func programName(word string) string {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(word, `\`, "/")))
	for _, ext := range []string{".exe", ".com", ".cmd", ".bat", ".ps1"} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}

func (g *guard) run(segs []shellSeg, kind shellKind, depth int) {
	if depth > 6 {
		g.raise(CommandAsk, "a deeply nested command that cannot be checked")
		return
	}
	for _, seg := range segs {
		g.segment(seg, kind, depth)
	}
}

// prefixWrappers only run the next program. A delete behind one of them is
// never allowed silently.
var prefixWrappers = map[string]bool{"sudo": true, "doas": true, "nohup": true, "time": true, "exec": true, "command": true,
	"builtin": true, "nice": true, "ionice": true, "busybox": true, "env": true, "call": true, "start": true, "runas": true, "chroot": true, "timeout": true, "stdbuf": true}

// loopWords start loops; a delete inside one acts on a list known only at run time.
var loopWords = map[string]bool{"for": true, "foreach": true, "foreach-object": true, "%": true, "while": true, "until": true, "do": true, "forfiles": true}

// dynamicPrograms run text built at run time.
var dynamicPrograms = map[string]bool{"iex": true, "invoke-expression": true, "eval": true, "invoke-command": true, "icm": true, "source": true, ".": true}

func (g *guard) segment(sg shellSeg, kind shellKind, depth int) {
	seg := sg.words
	// Wrappers that just run the next program.
	for len(seg) > 0 {
		name := programName(seg[0])
		if !prefixWrappers[name] {
			break
		}
		g.wrapped = name
		seg = seg[1:]
		for len(seg) > 0 && (strings.HasPrefix(seg[0], "-") || name == "env" && strings.Contains(seg[0], "=") || name == "timeout" && isNumber(seg[0])) {
			seg = seg[1:]
		}
	}
	if len(seg) == 0 {
		return
	}
	if strings.HasPrefix(seg[0], "(") && strings.HasSuffix(seg[0], ")") {
		// A subshell or grouped command: check what it runs.
		g.run(splitShell(strings.TrimSuffix(strings.TrimPrefix(seg[0], "("), ")"), kind), kind, depth+1)
		seg = seg[1:]
		if len(seg) == 0 {
			return
		}
	}
	name := programName(seg[0])
	args := seg[1:]
	if loopWords[name] || loopWords[strings.TrimSuffix(name, ";")] {
		g.inLoop = true
		if name == "do" {
			// bash: "do rm ..." - the body follows in this command.
			g.segment(shellSeg{words: args}, kind, depth+1)
			return
		}
		for i, a := range args {
			// cmd: "for ... do rd ..." keeps the body after "do".
			if strings.EqualFold(a, "do") && i+1 < len(args) {
				g.segment(shellSeg{words: args[i+1:]}, kind, depth+1)
				return
			}
			// PowerShell: ForEach-Object { Remove-Item /c/Users/ASRock/Desktop/SuperCli/wt-orch-status } - a delete in a
			// script block runs on items known only at run time.
			if isDeleteProgram(programName(strings.Trim(a, "{}")), kind) {
				g.raise(CommandAsk, "a delete inside a loop")
				return
			}
		}
		return
	}
	if dynamicPrograms[name] {
		g.raise(CommandAsk, "a command built at run time ("+name+")")
		return
	}
	if kind != shellArgv && dynamicValue.MatchString(seg[0]) {
		g.raise(CommandAsk, "a program named by a variable or substitution ("+seg[0]+")")
		return
	}
	if text, inner, ok := wrappedCommand(name, args); ok {
		if inner == shellUnknown {
			g.raise(CommandAsk, "an encoded or indirect shell command whose content cannot be checked")
			return
		}
		g.run(splitShell(text, inner), inner, depth+1)
		return
	}
	switch {
	case isCd(name):
		g.cd(args, kind)
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
		if isDeleteProgram(programName(firstNonFlag(args)), kind) {
			g.raise(CommandAsk, "a delete whose targets come from another command's output")
		}
	case name == "forfiles":
		g.raise(CommandAsk, "a command run for each of a set of files")
	default:
		if isDeleteProgram(name, kind) {
			g.delete(name, args, kind, sg.piped)
		}
	}
}

func isNumber(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' && r != 's' && r != 'm' {
			return false
		}
	}
	return s != ""
}

func isCd(name string) bool {
	switch name {
	case "cd", "chdir", "pushd", "set-location", "sl", "push-location":
		return true
	}
	return false
}

// cd moves the tracked working directory; anything dynamic makes it unknown.
func (g *guard) cd(args []string, kind shellKind) {
	target := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		l := strings.ToLower(a)
		if l == "/d" || l == "-l" || l == "-p" || l == "--" {
			continue
		}
		if strings.HasPrefix(l, "-path") || strings.HasPrefix(l, "-literalpath") {
			if i+1 < len(args) {
				target = args[i+1]
			}
			break
		}
		target = a
		break
	}
	if target == "" {
		if kind != shellCmd {
			g.cwd = g.home() // "cd" alone goes home (sh, PowerShell)
		}
		return
	}
	if dynamicValue.MatchString(target) || target == "-" {
		g.cwd = ""
		return
	}
	if strings.HasPrefix(target, "~") {
		rest := strings.TrimLeft(target[1:], "/\\")
		if g.home() == "" || rest != "" && !strings.ContainsAny(target[1:2], "/\\") {
			g.cwd = "" // ~name or no home: unknown
			return
		}
		g.cwd = filepath.Join(g.home(), rest)
		return
	}
	g.cwd = g.resolve(target)
}

// wrappedCommand returns the command text a shell wrapper runs.
func wrappedCommand(name string, args []string) (string, shellKind, bool) {
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "ash", "fish":
		for i, a := range args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
				if i+1 < len(args) {
					return args[i+1], shellPOSIX, true
				}
				return "", shellUnknown, true
			}
		}
		if len(args) > 0 {
			return "", shellUnknown, true // a script file or stdin: not inspected
		}
	case "cmd":
		for i, a := range args {
			if l := strings.ToLower(a); l == "/c" || l == "/k" || l == "/r" {
				return strings.Join(args[i+1:], " "), shellCmd, true
			}
		}
	case "powershell", "pwsh", "powershell_ise":
		for i := 0; i < len(args); i++ {
			l := strings.ToLower(args[i])
			// -EncodedCommand: "-e", "-ec" and every prefix of
			// -encodedcommand, in any letter case.
			if l == "-e" || l == "-ec" || len(l) >= 3 && strings.HasPrefix("-encodedcommand", l) {
				return "", shellUnknown, true
			}
			if l == "-c" || len(l) >= 3 && strings.HasPrefix("-command", l) {
				return strings.Join(args[i+1:], " "), shellPwsh, true
			}
			if l == "-file" || l == "-f" {
				return "", shellUnknown, true // a script file: not inspected
			}
			if !strings.HasPrefix(l, "-") {
				// powershell "Remove-Item ..." (implicit -Command)
				return strings.Join(args[i:], " "), shellPwsh, true
			}
			switch {
			case strings.HasPrefix("-executionpolicy", l) && len(l) >= 3, l == "-ep",
				strings.HasPrefix("-windowstyle", l) && len(l) >= 2, strings.HasPrefix("-workingdirectory", l) && len(l) >= 3,
				strings.HasPrefix("-inputformat", l) && len(l) >= 3, strings.HasPrefix("-outputformat", l) && len(l) >= 3,
				strings.HasPrefix("-configurationname", l) && len(l) >= 4, strings.HasPrefix("-settingsfile", l) && len(l) >= 3:
				i++ // option value
			}
		}
	}
	return "", shellArgv, false
}

func firstNonFlag(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// isDeleteProgram: rm/unlink (POSIX), Remove-Item and its PowerShell aliases,
// del/erase/rd/rmdir (cmd), shred.
func isDeleteProgram(name string, kind shellKind) bool {
	switch name {
	case "rm", "unlink", "rmdir", "shred", "srm", "remove-item", "ri", "del", "erase", "rd", "rp", "clear-content", "clc":
		return true
	}
	return false
}

// delete checks every target of a delete command.
func (g *guard) delete(name string, args []string, kind shellKind, piped bool) {
	pwsh := kind == shellPwsh || name == "remove-item" || name == "ri" || name == "clear-content" || name == "clc"
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
			// PowerShell parameters are case-insensitive prefixes. Path and
			// LiteralPath values are targets; these others take a value.
			p := strings.TrimPrefix(l, "-")
			if p != "" && (strings.HasPrefix("path", p) || strings.HasPrefix("literalpath", p) && len(p) >= 2) && i+1 < len(args) {
				targets = append(targets, args[i+1])
				i++
			} else if p != "" && (strings.HasPrefix("include", p) && len(p) >= 2 || strings.HasPrefix("exclude", p) && len(p) >= 2 ||
				strings.HasPrefix("filter", p) && len(p) >= 2 || strings.HasPrefix("stream", p) && len(p) >= 2 || strings.HasPrefix("credential", p) && len(p) >= 2) && i+1 < len(args) {
				i++
			}
		case cmd && strings.HasPrefix(a, "/"):
			// /s /q /f /a: flags
		case !pwsh && !cmd && strings.HasPrefix(a, "-") && a != "-":
			// rm flags
		default:
			targets = append(targets, a)
		}
	}
	if piped {
		g.raise(CommandAsk, "a delete whose targets come through a pipe")
	}
	if g.inLoop {
		g.raise(CommandAsk, "a delete inside a loop")
	}
	if g.wrapped != "" {
		g.raise(CommandAsk, "a delete run through "+g.wrapped)
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
	if dynamicValue.MatchString(t) || strings.HasPrefix(t, "(") {
		g.raise(CommandAsk, what+" of a path known only when the command runs ("+t+")")
		return
	}
	if strings.HasPrefix(t, "~") {
		rest := strings.TrimLeft(t[1:], `/\`)
		user := t[1:]
		if i := strings.IndexAny(user, `/\`); i >= 0 {
			user = user[:i]
		}
		if user != "" && user != "+" && user != "-" {
			// ~name: another user's home.
			g.raise(CommandBlock, what+" in another user's home folder ("+raw+")")
			return
		}
		if g.home() == "" {
			g.raise(CommandAsk, what+" of a path in the home folder ("+raw+")")
			return
		}
		t = filepath.Join(g.home(), rest)
	}
	// A glob deletes what its folder contains: judge that folder, and the
	// folder itself when the glob is everything in it.
	dir, container := t, false
	if i := strings.IndexAny(t, "*?["); i >= 0 {
		pattern := t
		if j := strings.LastIndexAny(t[:i], `/\`); j >= 0 {
			pattern = t[j+1:]
			dir = t[:j+1]
		} else {
			dir = "."
		}
		container = pattern != "*" && pattern != "*.*" && pattern != ".*" && pattern != "**"
	}
	abs := g.resolve(dir)
	if abs == "" {
		g.raise(CommandAsk, what+" relative to a working directory that cannot be known ("+raw+")")
		return
	}
	verdict, why := g.judge(abs, container)
	if verdict != CommandAllow {
		g.raise(verdict, what+" of "+why+" ("+raw+")")
	}
}

// resolve turns a path into a clean absolute one against the tracked working
// directory. "" when the working directory is unknown.
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
	case runtime.GOOS == "windows" && strings.HasPrefix(p, `\`):
		vol := filepath.VolumeName(g.cwd)
		if vol == "" {
			vol = filepath.VolumeName(g.base)
		}
		if vol == "" {
			vol = "C:"
		}
		return filepath.Clean(vol + p)
	case runtime.GOOS == "windows" && len(p) == 2 && p[1] == ':':
		return filepath.Clean(p + `\`)
	case g.cwd == "":
		return ""
	}
	return filepath.Clean(filepath.Join(g.cwd, p))
}

// judge decides a delete of abs: Block for unambiguous disasters, Allow for a
// literal path inside the project, Ask for the rest.
func (g *guard) judge(abs string, container bool) (CommandVerdict, string) {
	home := g.home()
	if isRoot(abs) {
		return CommandBlock, "a drive or filesystem root"
	}
	if home != "" && (samePath(abs, home) || within(home, abs) || samePath(filepath.Dir(abs), home)) {
		return CommandBlock, "the home folder, its parents or a folder directly in it"
	}
	for _, sys := range systemDirs() {
		if samePath(abs, sys) || within(abs, sys) || within(sys, abs) {
			return CommandBlock, "a system folder"
		}
	}
	for _, users := range userRoots() {
		if samePath(abs, users) || within(users, abs) || samePath(filepath.Dir(abs), users) {
			return CommandBlock, "a user's home folder"
		}
	}
	if g.base == "" {
		return CommandAsk, "a path while the project folder is unknown"
	}
	if within(g.base, abs) || samePath(abs, g.base) && !container {
		return CommandBlock, "the whole project"
	}
	if !within(abs, g.base) && !samePath(abs, g.base) {
		return CommandAsk, "a path outside the project"
	}
	if rel, err := filepath.Rel(g.base, abs); err == nil {
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if strings.EqualFold(part, ".git") {
				return CommandBlock, "the project's git history (.git)"
			}
		}
	}
	return CommandAllow, ""
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

// systemDirs hold the operating system and installed programs.
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
			if o == "." || o == "*" || o == ":/" {
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
		if (a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir") && i+1 < len(args) && isDeleteProgram(programName(args[i+1]), shellPOSIX) {
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

// shellSeg is one command of a command line; piped: its input is the output
// of the previous command ("a | b").
type shellSeg struct {
	words []string
	piped bool
}

// splitShell splits command text into commands (word lists) with the quoting
// rules of the given shell. Separators inside quotes are text; a
// parenthesized group stays one word "(...)".
func splitShell(text string, kind shellKind) []shellSeg {
	var segs []shellSeg
	var words []string
	var cur strings.Builder
	inWord, piped := false, false
	flushWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	flushSeg := func(nextPiped bool) {
		flushWord()
		if len(words) > 0 {
			segs = append(segs, shellSeg{words: words, piped: piped})
		}
		words = nil
		piped = nextPiped
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
		case c == '(' && kind != shellCmd:
			// A group: $(...), (...), keep it together as text.
			inWord = true
			depth := 0
			for ; i < len(r); i++ {
				if r[i] == '(' {
					depth++
				} else if r[i] == ')' {
					depth--
				}
				cur.WriteRune(r[i])
				if depth == 0 {
					break
				}
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
		case c == '|':
			if i+1 < len(r) && r[i+1] == '|' {
				i++
				flushSeg(false)
			} else {
				flushSeg(true)
			}
		case c == ';' && kind != shellCmd, c == '&', c == '\n', c == '\r':
			flushSeg(false)
		case c == ' ' || c == '\t':
			flushWord()
		case c == '#' && kind == shellPOSIX && !inWord:
			for i < len(r) && r[i] != '\n' {
				i++
			}
			flushSeg(false)
		default:
			inWord = true
			cur.WriteRune(c)
		}
	}
	flushSeg(false)
	return segs
}
