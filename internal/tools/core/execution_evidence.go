package core

import (
	"sync/atomic"
	"time"
)

var executionSequence atomic.Uint64

// NextExecutionSequence orders executor starts and observed edits in this host.
// Wall-clock equality/skew must not turn a fresh check into a cached process.
func NextExecutionSequence() uint64 { return executionSequence.Add(1) }

// ExecutionEvidence contains only facts observed by a command executor. Streams
// share the executor's bounded immutable capture; this does not add a wire copy.
// The effective executor environment is represented only by an identity digest.
type ExecutionEvidence struct {
	Command         []string
	Workdir         string
	Environment     [32]byte
	StartedAt       time.Time
	StartedSequence uint64
	Completed       bool
	ExitCode        int
	OutputComplete  bool
	Stdout          string
	Stderr          string
}

// VerificationState is the small cached part of a goal that changes completion
// admission. It contains no notes, prompts, test claims or user data.
type VerificationState struct {
	Scope       string
	Done, Total int
	Status      string
}
