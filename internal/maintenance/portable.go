package maintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// BeginPortableRun adds a shared native lease for other CLI/web processes. When
// housekeeping owns the exclusive lease, byte 1 requests cancellation; it is
// polled only during maintenance. No PID/age inference and no stale lock removal.
func BeginPortableRun(ctx context.Context, root string) (func(), error) {
	end := BeginRun()
	if root == "" {
		return end, nil
	}
	file, err := openActivityFile(root)
	if err != nil {
		end()
		return nil, err
	}
	for {
		unlock, err := lockActivityFile(file, false)
		if err == nil {
			return func() { unlock(); file.Close(); end() }, nil
		}
		if !errors.Is(err, errActivityBusy) {
			file.Close()
			end()
			return nil, err
		}
		if _, err := file.WriteAt([]byte{1}, 1); err != nil {
			file.Close()
			end()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			end()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func WhilePortableIdle(parent context.Context, root string, fn func(context.Context)) bool {
	return WhileIdle(parent, func(local context.Context) {
		file, err := openActivityFile(root)
		if err != nil {
			return
		}
		defer file.Close()
		unlock, err := lockActivityFile(file, true)
		if err != nil {
			return
		}
		defer unlock()
		if _, err := file.WriteAt([]byte{0}, 1); err != nil {
			return
		}
		ctx, cancel := context.WithCancel(local)
		done := make(chan struct{})
		go func() {
			defer close(done)
			tick := time.NewTicker(20 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					var request [1]byte
					if _, err := file.ReadAt(request[:], 1); err != nil || request[0] != 0 {
						cancel()
						return
					}
				}
			}
		}()
		defer func() { cancel(); <-done }()
		fn(ctx)
	})
}

func openActivityFile(root string) (*os.File, error) {
	path := filepath.Join(root, ".housekeeping.lock")
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("housekeeping lease is not a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
}

var errActivityBusy = errors.New("housekeeping lease busy")
