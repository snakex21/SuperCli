package checkpoint

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"supercli/internal/system/childproc"
)

// defaultCheckpointOutputDrainGrace bounds how long a pipe may stay open with
// no read progress after native exit. It is not a command runtime limit.
const defaultCheckpointOutputDrainGrace = time.Second

// checkpointOutputDrainGraceNanos is variable only so tests can pin it
// explicitly; production code always uses the default.
var checkpointOutputDrainGraceNanos atomic.Int64

func init() { checkpointOutputDrainGraceNanos.Store(int64(defaultCheckpointOutputDrainGrace)) }

func checkpointOutputDrainGrace() time.Duration {
	return time.Duration(checkpointOutputDrainGraceNanos.Load())
}

func configureCheckpointCommand(cmd *exec.Cmd) {
	childproc.HideWindow(cmd)
	cmd.WaitDelay = checkpointOutputDrainGrace()
}

// Caller-owned stdout prevents Cmd.Wait from closing StdoutPipe before the
// scanner has consumed the final bytes. Waiting concurrently observes native
// exit even when an inherited writer prevents the scanner from seeing EOF.
type checkpointCommandStream struct {
	Stdout     *checkpointStdout
	ctx        context.Context
	readDone   chan struct{}
	waited     chan error
	stopCancel func() bool
}

// checkpointStdout records when the consumer is blocked in Read without
// receiving bytes. Time the consumer spends processing data it already read
// never counts against the drain grace: only a pipe that delivers nothing to a
// waiting reader (for example one held open by a grandchild) is bounded.
type checkpointStdout struct {
	file *os.File
	// blockedSince is the UnixNano time the current Read started, or 0 while
	// the consumer is outside Read.
	blockedSince atomic.Int64
}

func (r *checkpointStdout) Read(p []byte) (int, error) {
	r.blockedSince.Store(time.Now().UnixNano())
	n, err := r.file.Read(p)
	r.blockedSince.Store(0)
	return n, err
}

func (r *checkpointStdout) Close() error { return r.file.Close() }

// stalledFor reports how long the consumer has been waiting in Read without
// progress, counting no earlier than since.
func (r *checkpointStdout) stalledFor(since, now time.Time) time.Duration {
	blocked := r.blockedSince.Load()
	if blocked == 0 {
		return 0
	}
	start := time.Unix(0, blocked)
	if start.Before(since) {
		start = since
	}
	return now.Sub(start)
}

func startCheckpointCommandStream(ctx context.Context, cmd *exec.Cmd) (*checkpointCommandStream, error) {
	file, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = writer
	if err := cmd.Start(); err != nil {
		_ = writer.Close()
		_ = file.Close()
		return nil, err
	}
	_ = writer.Close() // Only the native process/its descendants own writers.
	stdout := &checkpointStdout{file: file}
	grace := checkpointOutputDrainGrace()
	stream := &checkpointCommandStream{Stdout: stdout, ctx: ctx, readDone: make(chan struct{}), waited: make(chan error, 1)}
	stream.stopCancel = context.AfterFunc(ctx, func() { _ = file.Close() })
	go func() {
		err := cmd.Wait() // Exactly one native waiter; stderr uses Cmd.WaitDelay.
		exited := time.Now()
		timer := time.NewTimer(grace)
		defer timer.Stop()
		for {
			select {
			case <-stream.readDone:
			case <-ctx.Done():
				_ = file.Close()
				err = errors.Join(err, ctx.Err())
			case now := <-timer.C:
				stalled := stdout.stalledFor(exited, now)
				if stalled < grace {
					// Reads are progressing or the consumer is busy with data
					// it already has; re-arm for the remaining stall budget.
					timer.Reset(grace - stalled)
					continue
				}
				// A forced EOF is never a successful complete checkpoint protocol.
				_ = file.Close()
				err = errors.Join(err, exec.ErrWaitDelay)
			}
			break
		}
		stream.waited <- err
	}()
	return stream, nil
}

// Call after consuming stdout (or after canceling because the protocol failed).
// Joining the waiter precedes reading stderr or publishing any successful result.
func (s *checkpointCommandStream) Wait() error {
	close(s.readDone)
	err := <-s.waited
	s.stopCancel()
	_ = s.Stdout.Close()
	return errors.Join(err, s.ctx.Err())
}
