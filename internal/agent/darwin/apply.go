package darwin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// candidateBranchRE matches the branch names WorktreeManager.Create makes.
// User-facing apply/discard only ever touch branches of this shape.
var candidateBranchRE = regexp.MustCompile(`^darwin-[0-9]+-[0-9a-f]+$`)

// IsCandidateBranch reports whether name is a Darwin candidate branch.
func IsCandidateBranch(name string) bool { return candidateBranchRE.MatchString(name) }

// ApplyBranch merges a kept candidate branch into the current branch of the
// repo at base. It is the user's explicit "apply" action, so unlike
// WorktreeManager.Merge it does not refuse main/master. A merge that fails
// midway is aborted, leaving the working tree as it was; an already running
// merge of the user's is never touched.
func ApplyBranch(ctx context.Context, base, branch string) error {
	if !IsCandidateBranch(branch) {
		return fmt.Errorf("darwin: apply: %q is not a darwin branch", branch)
	}
	if _, err := runGitCtx(ctx, base, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("darwin: apply: branch %s no longer exists", branch)
	}
	if _, err := runGitCtx(ctx, base, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); err == nil {
		return fmt.Errorf("darwin: apply: a merge is already in progress; finish or abort it first")
	}
	msg := "darwin: apply " + branch
	args := []string{"merge", "--no-ff", "--no-edit", "-m", msg, branch}
	out, err := runGitCtx(ctx, base, args...)
	if err != nil && isMissingIdentity(out) {
		out, err = runGitCtx(ctx, base, append([]string{"-c", "user.name=SuperCli Darwin", "-c", "user.email=darwin@supercli.invalid"}, args...)...)
	}
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	if _, mErr := runGitCtx(context.Background(), base, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); mErr == nil {
		_, _ = runGitCtx(context.Background(), base, "merge", "--abort")
	}
	if strings.Contains(strings.ToLower(detail), "conflict") {
		return fmt.Errorf("%w: %s (merge aborted, nothing changed)", ErrMergeConflict, branch)
	}
	return fmt.Errorf("darwin: apply %s: %s", branch, detail)
}

// DeleteBranches force-deletes the given Darwin candidate branches in the repo
// at base. Non-candidate names and branches that are already gone are
// skipped; the first real failure is returned after trying all of them.
func DeleteBranches(ctx context.Context, base string, branches []string) error {
	var first error
	for _, b := range branches {
		if !IsCandidateBranch(b) {
			continue
		}
		if _, err := runGitCtx(ctx, base, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err != nil {
			continue
		}
		if out, err := runGitCtx(ctx, base, "branch", "-D", b); err != nil && first == nil {
			first = fmt.Errorf("darwin: delete branch %s: %v: %s", b, err, strings.TrimSpace(string(out)))
		}
	}
	return first
}
