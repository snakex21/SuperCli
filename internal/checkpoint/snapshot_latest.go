package checkpoint

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Finalize only after a committed record or a fully drained verified no-op.
// Other live captures have their own active roots, so latest can point to the
// minimal recorded tree without retaining unrelated full workspace contents.
// Caller holds StoreIO. A release failure keeps the original active pins.
func (t *Turn) finishSnapshotLatestLocked(ctx context.Context) error {
	if !t.touched || t.snapshotAfter == "" {
		return nil
	}
	if t.completed != nil {
		_, err := t.manager.git(ctx, "update-ref", "refs/supercli/latest", t.completed.After)
		if err != nil {
			return err
		}
		return t.manager.accountRefsLocked(1)
	}
	cmd := t.manager.gitCommand(ctx, "rev-parse", "--verify", "--quiet", "refs/supercli/latest")
	output, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil
		}
		return err
	}
	if strings.TrimSpace(string(output)) != t.snapshotAfter {
		return nil
	}
	_, err = t.manager.git(ctx, "update-ref", "-d", "refs/supercli/latest", t.snapshotAfter)
	return err
}
