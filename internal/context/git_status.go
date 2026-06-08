package context

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// GitStatusLoader runs `git status --short` and `git log -1
// --oneline` in the home directory and packages them into a
// single source. When home is not a git repo, the source body
// is empty and the loader returns no error.
type GitStatusLoader struct {
	Home string
	// Timeout caps the git invocation. Zero = 2s.
	Timeout time.Duration
}

// NewGitStatusLoader returns a loader with sensible defaults.
func NewGitStatusLoader(home string) *GitStatusLoader {
	return &GitStatusLoader{Home: home, Timeout: 2 * time.Second}
}

// Name implements Loader.
func (l *GitStatusLoader) Name() string { return "git_status" }

// Priority is high because dirty state is a strong signal for
// the model's first action (commit, stash, branch).
func (l *GitStatusLoader) Priority() int { return 60 }

// Load runs git and returns the result.
func (l *GitStatusLoader) Load() (Source, error) {
	if l.Home == "" {
		return Source{}, fmt.Errorf("context.GitStatusLoader: home is empty")
	}
	timeout := l.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	branch := l.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	short := l.run(ctx, "status", "--short")
	log := l.run(ctx, "log", "-1", "--oneline")

	if branch == "" && short == "" && log == "" {
		// Not a git repo, or all commands failed.
		return Source{Name: l.Name(), Body: ""}, nil
	}

	var b strings.Builder
	if branch != "" {
		fmt.Fprintf(&b, "branch: %s\n", branch)
	}
	if short != "" {
		fmt.Fprintf(&b, "status:\n%s\n", indentLines(short))
	} else {
		b.WriteString("status: clean\n")
	}
	if log != "" {
		fmt.Fprintf(&b, "last commit: %s\n", log)
	}
	return Source{
		Name:     l.Name(),
		Body:     strings.TrimRight(b.String(), "\n"),
		Priority: l.Priority(),
		TokenCap: 300,
	}, nil
}

func (l *GitStatusLoader) run(ctx context.Context, args ...string) string {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = l.Home
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n")
}

func indentLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
