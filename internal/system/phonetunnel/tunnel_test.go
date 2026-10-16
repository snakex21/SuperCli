package phonetunnel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixtureInstaller(t *testing.T) Installer {
	t.Helper()
	data := []byte("offline pinned tool")
	h := sha256.Sum256(data)
	i := Installer{DataDir: t.TempDir(), Platform: "windows/amd64", SHA256: hex.EncodeToString(h[:]), Size: int64(len(data))}
	if err := os.MkdirAll(filepath.Dir(i.Path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(i.Path(), data, 0600); err != nil {
		t.Fatal(err)
	}
	return i
}
func TestInstallerOffline(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		consent, good bool
	}{{"consent", "offline pinned tool", false, false}, {"verified", "offline pinned tool", true, true}, {"bad-hash", "wrong pinned tool!!", true, false}, {"truncated", "short", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			i := fixtureInstaller(t)
			if err := os.Remove(i.Path()); err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; io.WriteString(w, tc.body) }))
			defer server.Close()
			i.URL = server.URL
			var n int64
			_, err := i.Ensure(context.Background(), tc.consent, func(done, total int64) { n = done })
			if tc.good {
				if err != nil || !i.Available() || n != i.Size {
					t.Fatalf("err=%v available=%v progress=%d", err, i.Available(), n)
				}
				if _, err = i.Ensure(context.Background(), false, nil); err != nil || calls != 1 {
					t.Fatalf("cache: calls=%d err=%v", calls, err)
				}
			} else {
				if err == nil {
					t.Fatal("expected rejection")
				}
				if i.Available() {
					t.Fatal("invalid file installed")
				}
				matches, _ := filepath.Glob(filepath.Join(filepath.Dir(i.Path()), "download-*"))
				if len(matches) != 0 {
					t.Fatal("temporary file leaked")
				}
				if !tc.consent && (!errors.Is(err, ErrNeedDownload) || calls != 0) {
					t.Fatal("download without consent")
				}
			}
		})
	}
	i := fixtureInstaller(t)
	i.Platform = "linux/amd64"
	if _, err := i.Ensure(context.Background(), true, nil); err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("platform error=%v", err)
	}
}

func TestInstallerCacheReusesVerifiedFile(t *testing.T) {
	i := fixtureInstaller(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("cached tool must not be downloaded again")
	}))
	defer server.Close()
	i.URL = server.URL
	for n := 0; n < 3; n++ {
		if _, err := i.Ensure(context.Background(), true, nil); err != nil {
			t.Fatal(err)
		}
	}
	// A changed file (same size, new mtime) must be re-verified and rejected.
	if err := os.WriteFile(i.Path(), []byte("tampered pinned too"), 0600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(i.Path(), later, later); err != nil {
		t.Fatal(err)
	}
	if i.Available() {
		t.Fatal("modified file accepted from cache")
	}
}

func TestManagerCachedToolSkipsDownloadingState(t *testing.T) {
	m := New(t.TempDir())
	m.Installer = fixtureInstaller(t)
	p := &fakeProcess{reader: strings.NewReader("https://test-phone.trycloudflare.com\n"), done: make(chan struct{})}
	m.Factory = func(path, target string) (Process, error) { return p, nil }
	if err := m.Start(context.Background(), "http://127.0.0.1:1", false, nil); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("stop")
	if st := m.Status(); st.State == "downloading" {
		t.Fatalf("cached tool reported download: %+v", st)
	}
	waitState(t, m, "on")
}

type fakeProcess struct {
	reader io.Reader
	done   chan struct{}
	once   sync.Once
}

func (p *fakeProcess) Stderr() io.Reader { return p.reader }
func (p *fakeProcess) Wait() error       { <-p.done; return errors.New("fake exit") }
func (p *fakeProcess) Kill() error       { p.once.Do(func() { close(p.done) }); return nil }
func waitState(t *testing.T, m *Manager, state string) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st := m.Status()
		if st.State == state {
			return st
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("wanted %s, got %+v", state, m.Status())
	return Status{}
}
func TestManagerLifecycle(t *testing.T) {
	for _, mode := range []string{"stop", "idle", "death", "timeout", "factory"} {
		t.Run(mode, func(t *testing.T) {
			m := New(t.TempDir())
			m.Installer = fixtureInstaller(t)
			m.StartupTimeout = 20 * time.Millisecond
			p := &fakeProcess{reader: strings.NewReader("https://test-phone.trycloudflare.com\n"), done: make(chan struct{})}
			if mode == "timeout" {
				p.reader = strings.NewReader("no URL")
			}
			if mode == "idle" {
				m.IdleTimeout = 60 * time.Millisecond
			}
			m.Factory = func(path, target string) (Process, error) {
				if mode == "factory" {
					return nil, errors.New("start failed")
				}
				return p, nil
			}
			if err := m.Start(context.Background(), "http://127.0.0.1:1", false, nil); err != nil {
				t.Fatal(err)
			}
			defer m.Stop("stop")
			if mode == "timeout" || mode == "factory" {
				waitState(t, m, "error")
				return
			}
			st := waitState(t, m, "on")
			if st.Host != "test-phone.trycloudflare.com" {
				t.Fatal(st)
			}
			if err := m.Start(context.Background(), "", false, nil); err == nil {
				t.Fatal("duplicate start accepted")
			}
			switch mode {
			case "stop":
				m.Stop("stop")
				if m.Status().Host != "" || m.Status().Reason != "stop" {
					t.Fatal(m.Status())
				}
			case "death":
				p.Kill()
				waitState(t, m, "error")
			case "idle":
				time.Sleep(30 * time.Millisecond)
				m.Touch()
				if !m.Status().ExpiresAt.After(st.ExpiresAt) {
					t.Fatal("touch did not extend idle timeout")
				}
				if waitState(t, m, "off").Reason != "idle" {
					t.Fatal(m.Status())
				}
			}
			select {
			case <-m.ActiveContext().Done():
			default:
				t.Fatal("active context not cancelled")
			}
		})
	}
}
