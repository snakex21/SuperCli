//go:build !windows

package processsession

func guardForeground(int) func() { return func() {} }
