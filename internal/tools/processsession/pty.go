package processsession

import "io"

type ptyProcess interface {
	io.ReadWriteCloser
	PID() int
	Wait() (int, error)
	Kill() error
	Resize(columns, rows int) error
}
