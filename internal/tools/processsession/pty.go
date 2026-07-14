package processsession

import "io"

type ptyProcess interface {
	io.ReadWriteCloser
	Wait() (int, error)
	Kill() error
	Resize(columns, rows int) error
}
