package webgui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"supercli/internal/system/phonetunnel"
)

var ErrPhoneNeedDownload = phonetunnel.ErrNeedDownload

// runPhoneProtocol is intentionally independent of desktop single-instance state.
func (s *Server) runPhoneProtocol(ctx context.Context, allowDownload bool, input io.Reader, output io.Writer) error {
	var mu sync.Mutex
	line := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(output, format+"\n", args...)
	}
	if !s.phone.manager.Available() && !allowDownload {
		// Ensure distinguishes an unsupported platform from a missing pinned tool.
		_, err := s.phone.manager.Installer.Ensure(ctx, false, nil)
		if errors.Is(err, phonetunnel.ErrNeedDownload) {
			line("SUPERCLI_PHONE_NEED_DOWNLOAD %d", phonetunnel.DownloadSize)
		} else {
			line("SUPERCLI_PHONE_CLOSED error")
		}
		return err
	}
	s.phone.mu.Lock()
	s.phone.onPair = func(url string) { line("SUPERCLI_PHONE_URL %s", url) }
	s.phone.mu.Unlock()
	defer func() { s.phone.mu.Lock(); s.phone.onPair = nil; s.phone.mu.Unlock() }()
	if err := s.phone.manager.Start(ctx, s.phone.target, allowDownload, func(n, total int64) { line("SUPERCLI_PHONE_PROGRESS %d %d", n, total) }); err != nil {
		return err
	}
	defer s.phone.manager.Stop("stop")
	commands := make(chan string)
	go func() {
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			select {
			case commands <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
		close(commands)
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	ready := false
	pair := func() error {
		_, err := s.phone.pair()
		return err
	}
	for {
		select {
		case <-ctx.Done():
			line("SUPERCLI_PHONE_CLOSED stop")
			return nil
		case command, ok := <-commands:
			if !ok || command == "stop" {
				s.phone.manager.Stop("stop")
				line("SUPERCLI_PHONE_CLOSED stop")
				return nil
			}
			if command == "pair" && ready {
				if err := pair(); err != nil {
					return err
				}
			}
		case <-ticker.C:
			st := s.phone.manager.Status()
			if st.State == "on" && !ready {
				if err := pair(); err != nil {
					return err
				}
				ready = true
			}
			if st.State == "off" || st.State == "error" {
				reason := st.Reason
				if reason == "" {
					reason = "error"
				}
				line("SUPERCLI_PHONE_CLOSED %s", reason)
				if st.Error != "" {
					return errors.New(st.Error)
				}
				return nil
			}
		}
	}
}

func (s *Server) runPhone(ctx context.Context, allowDownload bool) error {
	return s.runPhoneProtocol(ctx, allowDownload, os.Stdin, os.Stdout)
}
