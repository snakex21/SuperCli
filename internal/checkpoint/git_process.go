package checkpoint

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"supercli/internal/system/childproc"
)

// This is an output-drain bound after native exit, not a command runtime limit.
const checkpointOutputDrainGrace = time.Second

func configureCheckpointCommand(cmd *exec.Cmd) {
	childproc.HideWindow(cmd)
	cmd.WaitDelay = checkpointOutputDrainGrace
}

// Caller-owned stdout prevents Cmd.Wait from closing StdoutPipe before the
// scanner has consumed the final bytes. Waiting concurrently observes native
// exit even when an inherited writer prevents the scanner from seeing EOF.
type checkpointCommandStream struct {
	Stdout     *os.File
	ctx        context.Context
	readDone   chan struct{}
	waited     chan error
	stopCancel func() bool
}

func startCheckpointCommandStream(ctx context.Context, cmd *exec.Cmd) (*checkpointCommandStream, error) {
	stdout, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = writer
	if err := cmd.Start(); err != nil {
		_ = writer.Close()
		_ = stdout.Close()
		return nil, err
	}
	_ = writer.Close() // Only the native process/its descendants own writers.
	stream := &checkpointCommandStream{Stdout: stdout, ctx: ctx, readDone: make(chan struct{}), waited: make(chan error, 1)}
	stream.stopCancel = context.AfterFunc(ctx, func() { _ = stdout.Close() })
	go func() {
		err := cmd.Wait() // Exactly one native waiter; stderr uses Cmd.WaitDelay.
		timer := time.NewTimer(checkpointOutputDrainGrace)
		defer timer.Stop()
		select {
		case <-stream.readDone:
		case <-ctx.Done():
			_ = stdout.Close()
			err = errors.Join(err, ctx.Err())
		case <-timer.C:
			// A forced EOF is never a successful complete checkpoint protocol.
			_ = stdout.Close()
			err = errors.Join(err, exec.ErrWaitDelay)
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
