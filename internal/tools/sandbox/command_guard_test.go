package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckCommandMatrix(t *testing.T) {
	home, _ := os.UserHomeDir()
	parent := t.TempDir()
	project := filepath.Join(parent, "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	homeChild := filepath.Join(home, "Documents")
	block := [][]string{
		{"rm", "-rf", "/"},
		{"rm", "-rf", "~"},
		{"rm", "-rf", "~root"},
		{"rm", "-rf", "*"},
		{"rm", "-r", "-f", ".."},
		{"rm", "-rf", "src/../.."},
		{"rm", "-rf", ".git"},
		{"rm", "-rf", homeChild},
		{"cmd", "/c", `rd /s /q C:\`},
		{"cmd", "/c", `del /s /q C:\*`},
		{"powershell", "-NoProfile", "-Command", `Remove-Item -Recurse -Force C:\`},
		{"powershell", "-Command", `Remove-Item -r C:\Users\me`},
		{"powershell", "-Command", `Remove-Item -Rec -Path C:\Users\me`},
		{"pwsh", "-c", `ri -Recurse C:\Users\me`},
		{"pwsh", "-c", `Get-ChildItem; rm -r -fo C:\Windows`},
		{"bash", "-c", "cd /tmp && rm -rf /"},
		{"bash", "-lc", "find / -delete"},
		{"bash", "-c", "cd ~ && rm -rf " + filepath.Base(homeChild)},
		{"bash", "-c", "cd .. && rm -rf proj"},
		{"cmd", "/c", "cd /d " + home + " && rd /s /q Documents"},
		{"busybox", "rm", "-rf", "/"},
		{"format", "D:"},
		{"diskpart"},
		{"shutdown", "/s", "/t", "0"},
		{"dd", "if=/dev/zero", "of=/dev/sda"},
		{"git", "push", "--force", "origin", "main"},
		{"git", "push", "origin", "+main"},
		{"sudo", "rm", "-rf", "/"},
		{"env", "A=1", "rm", "-rf", "/"},
	}
	ask := [][]string{
		{"rm", "-rf", "${HOME}"},
		{"rm", "-rf", "$DIR"},
		{"bash", "-c", "rm -rf \"$BUILD_DIR\""},
		{"bash", "-c", "$CMD"},
		{"bash", "-c", "eval \"$X\""},
		{"cmd", "/c", "rd /s /q %TEMPDIR%"},
		{"cmd", "/c", "for /d %i in (*) do rd /s /q %i"},
		{"bash", "-c", "for d in build dist; do rm -rf \"$d\"; done"},
		{"powershell", "-Command", "Remove-Item -Recurse $env:OUT"},
		{"powershell", "-EncodedCommand", "SQBFAFgA"},
		{"powershell", "-ec", "SQBFAFgA"},
		{"powershell", "-en", "SQBFAFgA"},
		{"powershell", "-EC", "SQBFAFgA"},
		{"powershell", "-e", "SQBFAFgA"},
		{"pwsh", "-c", "iex (Get-Content x.ps1 -Raw)"},
		{"pwsh", "-c", "Invoke-Expression $s"},
		{"pwsh", "-c", "Get-ChildItem | Remove-Item -Recurse"},
		{"pwsh", "-c", `'C:\Users\me' | Remove-Item -Recurse -Force`},
		{"pwsh", "-c", "Remove-Item -Recurse -Force (Resolve-Path ~)"},
		{"pwsh", "-c", "Get-ChildItem *.log | ForEach-Object { Remove-Item $_ }"},
		{"bash", "-c", "ls | xargs rm -rf"},
		{"bash", "-c", "rm -rf $(cat list.txt)"},
		{"sudo", "rm", "-rf", "build"},
		{"rm", "-rf", filepath.Join(parent, "elsewhere")},
		{"bash", "-c", "cd \"$X\" && rm -rf build"},
		{"bash", "script.sh"},
		// git commands that discard uncommitted work: asked, not blocked.
		{"git", "reset", "--hard", "HEAD~3"},
		{"git", "reset", "--hard", "HEAD"},
		{"git", "-C", "x", "reset", "--hard"},
		{"git", "checkout", "."},
		{"git", "restore", "."},
		{"git", "clean", "-fdx"},
		{"git", "-c", "core.x=y", "clean", "-fdx"},
	}
	allow := [][]string{
		{"go", "test", "./..."},
		{"zig", "build", "client"},
		{"rm", "-rf", "build"},
		{"rm", "-rf", "./build"},
		{"rm", "-rf", "./zig-cache"},
		{"rm", "-f", "*.o"},
		{"rm", "-rf", "build/*"},
		{"cmd", "/c", "rd /s /q zig-out"},
		{"bash", "-c", "cd src && rm -rf gen"},
		{"git", "status"},
		{"git", "commit", "-m", "a; b"},
		{"git", "commit", "-m", "fix bug & reboot fixture"},
		{"git", "commit", "-m", "format code; shutdown hook | rm -rf /"},
		{"bash", "-c", `git commit -m "fix bug & reboot fixture"`},
		{"powershell", "-Command", `git commit -m 'a; Remove-Item -Recurse C:\'`},
		{"powershell", "-ExecutionPolicy", "Bypass", "-Command", "go test ./..."},
		{"git", "push", "origin", "main"},
		{"git", "reset", "--soft", "HEAD~1"},
		{"git", "clean", "-n"},
		{"powershell", "-Command", `Remove-Item build\out.txt`},
		{"echo", "rm -rf / is dangerous"},
		{"find", ".", "-name", "*.go"},
		{"env", "GOOS=linux", "go", "build", "./..."},
	}
	for _, argv := range block {
		if got := CheckCommand(argv, project, ""); got.Verdict != CommandBlock {
			t.Errorf("not blocked: %q -> %+v", argv, got)
		}
	}
	for _, argv := range ask {
		if got := CheckCommand(argv, project, ""); got.Verdict != CommandAsk {
			t.Errorf("not asked: %q -> %+v", argv, got)
		}
	}
	for _, argv := range allow {
		if got := CheckCommand(argv, project, ""); got.Verdict != CommandAllow {
			t.Errorf("ordinary command refused: %q -> %+v", argv, got)
		}
	}
	got := CheckCommand([]string{"git", "reset", "--hard", "HEAD"}, project, "")
	if !strings.Contains(got.Question, "uncommitted changes") || !strings.HasSuffix(got.Question, "Run it?") {
		t.Errorf("git reset --hard question = %q", got.Question)
	}
	if runtime.GOOS == "windows" {
		if got := CheckCommand([]string{"cmd", "/c", `rd /s /q "C:\Program Files"`}, project, ""); got.Verdict != CommandBlock {
			t.Errorf("system folder delete not blocked: %+v", got)
		}
	}
}

// Ordinary maintenance is not blocked; the real disasters still are.
func TestCheckCommandOrdinaryMaintenance(t *testing.T) {
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		argv []string
		want CommandVerdict
	}{
		// .git: lock files allowed, other contents asked, .git itself blocked.
		{[]string{"rm", "-f", ".git/index.lock"}, CommandAllow},
		{[]string{"cmd", "/c", `del .git\index.lock`}, CommandAllow},
		{[]string{"rm", "-f", ".git/refs/heads/main.lock"}, CommandAllow},
		{[]string{"rm", ".git/hooks/pre-commit"}, CommandAsk},
		{[]string{"rm", "-rf", ".git/hooks"}, CommandAsk},
		{[]string{"rm", "-f", ".git/*.lock"}, CommandAsk},
		{[]string{"rm", "-rf", ".git"}, CommandBlock},
		{[]string{"rm", "-rf", ".git/"}, CommandBlock},
		{[]string{"pwsh", "-c", "Remove-Item -Recurse -Force .git"}, CommandBlock},
		// git push.
		{[]string{"git", "push", "--force-with-lease"}, CommandAsk},
		{[]string{"git", "push", "--force-with-lease=main:abc123", "origin", "main"}, CommandAsk},
		{[]string{"git", "push", "--force-if-includes", "origin", "main"}, CommandAsk},
		{[]string{"git", "push", "origin", "--delete", "old-branch"}, CommandAsk},
		{[]string{"git", "push", "origin", "-d", "old-branch"}, CommandAsk},
		{[]string{"git", "push", "origin", ":old-branch"}, CommandAsk},
		{[]string{"git", "push", "--force", "origin", "main"}, CommandBlock},
		{[]string{"git", "push", "-f"}, CommandBlock},
		{[]string{"git", "push", "origin", "+main"}, CommandBlock},
		{[]string{"git", "push", "--force-with-lease", "--force"}, CommandBlock},
		{[]string{"git", "push", "origin", "main"}, CommandAllow},
		// Cache and tool folders in home: asked; home and other folders in it: blocked.
		{[]string{"rm", "-rf", "~/.cache"}, CommandAsk},
		{[]string{"rm", "-rf", "~/.npm"}, CommandAsk},
		{[]string{"rm", "-rf", "~/.gradle"}, CommandAsk},
		{[]string{"rm", "-rf", "~/.cache/*"}, CommandAsk},
		{[]string{"rm", "-rf", "~/.cache/go-build"}, CommandAsk},
		{[]string{"rm", "-rf", "~"}, CommandBlock},
		{[]string{"rm", "-rf", "~/Documents"}, CommandBlock},
		{[]string{"rm", "-rf", "~/.ssh"}, CommandBlock},
		{[]string{"rm", "-rf", "~/*"}, CommandBlock},
		// cipher: /w wipes, the rest is asked.
		{[]string{"cipher", "/w:C:"}, CommandBlock},
		{[]string{"cipher", "/W:C:\\"}, CommandBlock},
		{[]string{"cipher", "/e", "secret.txt"}, CommandAsk},
		{[]string{"cipher"}, CommandAsk},
		// Unchanged.
		{[]string{"rm", "-rf", "*"}, CommandBlock},
	}
	for _, c := range cases {
		if got := CheckCommand(c.argv, project, ""); got.Verdict != c.want {
			t.Errorf("%q -> %+v, want verdict %d", c.argv, got, c.want)
		}
	}
	if got := CheckCommand([]string{"git", "push", "--force-with-lease"}, project, ""); !strings.HasSuffix(got.Question, "Run it?") {
		t.Errorf("force-with-lease question = %q", got.Question)
	}
}

func TestCheckShellTextForInteractiveSessions(t *testing.T) {
	project := t.TempDir()
	if got := CheckShellText("cd src && rm -rf /\n", project, ""); got.Verdict != CommandBlock {
		t.Fatalf("typed rm -rf / not blocked: %+v", got)
	}
	if got := CheckShellText("go test ./...\n", project, ""); got.Verdict != CommandAllow {
		t.Fatalf("typed test refused: %+v", got)
	}
}

func TestSplitShellKeepsQuotedSeparators(t *testing.T) {
	segs := splitShell(`git commit -m "a & b; c | d" && echo 'x;y'`, shellPOSIX)
	if len(segs) != 2 || strings.Join(segs[0].words, "|") != "git|commit|-m|a & b; c | d" || strings.Join(segs[1].words, "|") != "echo|x;y" {
		t.Fatalf("segments = %+v", segs)
	}
	segs = splitShell("Get-ChildItem | Remove-Item -Recurse", shellPwsh)
	if len(segs) != 2 || segs[0].piped || !segs[1].piped {
		t.Fatalf("pipe not marked: %+v", segs)
	}
	segs = splitShell("Remove-Item -Path 'C:\\a b' -Recurse; Write-Host `\"x`\"", shellPwsh)
	if len(segs) != 2 || segs[0].words[2] != `C:\a b` {
		t.Fatalf("pwsh segments = %+v", segs)
	}
}
