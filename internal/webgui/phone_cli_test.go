package webgui

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPhoneCLIProtocol(t *testing.T) {
	for _, closeMode := range []string{"stop", "eof"} {
		t.Run(closeMode, func(t *testing.T) {
			s := NewServer(nil, false)
			setupTestPhone(t, s)
			s.phone.manager.Stop("stop")
			in, send := io.Pipe()
			output, writer := io.Pipe()
			defer send.Close()
			defer in.Close()
			defer output.Close()
			defer writer.Close()
			lines := make(chan string, 8)
			go func() {
				scanner := bufio.NewScanner(output)
				for scanner.Scan() {
					lines <- scanner.Text()
				}
				close(lines)
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- s.runPhoneProtocol(ctx, false, in, writer) }()
			next := func() string {
				t.Helper()
				select {
				case line := <-lines:
					return line
				case <-time.After(3 * time.Second):
					t.Fatal("protocol timeout")
					return ""
				}
			}
			first := next()
			if !strings.HasPrefix(first, "SUPERCLI_PHONE_URL https://test-phone.trycloudflare.com/.__supercli/phone#") {
				t.Fatal(first)
			}
			if _, err := io.WriteString(send, "pair\n"); err != nil {
				t.Fatal(err)
			}
			second := next()
			if !strings.HasPrefix(second, "SUPERCLI_PHONE_URL ") || first == second {
				t.Fatalf("token was not rotated: %s", second)
			}
			// Pairing through the local GUI must update the TUI's displayed link too.
			if _, err := s.phone.pair(); err != nil {
				t.Fatal(err)
			}
			third := next()
			if !strings.HasPrefix(third, "SUPERCLI_PHONE_URL ") || third == second {
				t.Fatalf("GUI token was not reported: %s", third)
			}
			if closeMode == "stop" {
				io.WriteString(send, "stop\n")
			} else {
				send.Close()
			}
			if line := next(); line != "SUPERCLI_PHONE_CLOSED stop" {
				t.Fatal(line)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if s.phone.manager.Status().State != "off" {
				t.Fatal("tunnel survived protocol exit")
			}
		})
	}
}
func TestPhoneCLINeedDownload(t *testing.T) {
	s := NewServer(nil, false)
	setupTestPhone(t, s)
	s.phone.manager.Stop("stop")
	if err := os.Remove(s.phone.manager.Installer.Path()); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := s.runPhoneProtocol(context.Background(), false, strings.NewReader(""), &output)
	if !errors.Is(err, ErrPhoneNeedDownload) || output.String() != "SUPERCLI_PHONE_NEED_DOWNLOAD 55325120\n" {
		t.Fatalf("output=%q err=%v", output.String(), err)
	}
}
