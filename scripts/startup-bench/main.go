// Command startup-bench measures how long supercli-web takes to show a
// usable interface: process start -> HTTP answering -> page script start ->
// session list shown -> last conversation's newest messages shown. It drives
// its own instance (own port and data directory) with the echo model and a
// headless Chrome/Edge, and prints the median of several runs plus the
// slowest /api requests of the last run.
//
//	go run ./scripts/startup-bench -exe supercli-web.exe -data DIR -runs 5
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"supercli/internal/testbrowser"
)

type sample struct {
	HTTP, Script, Sessions, History, Total time.Duration
}

func main() {
	exe := flag.String("exe", "supercli-web.exe", "supercli-web executable")
	dataDir := flag.String("data", "", "data directory (required; use a copy, never the user's)")
	runs := flag.Int("runs", 5, "measured runs")
	browserPath := flag.String("browser", "", "Chrome/Edge executable (default: auto)")
	flag.Parse()
	if *dataDir == "" {
		log.Fatal("-data is required")
	}
	abs, err := filepath.Abs(*exe)
	if err != nil {
		log.Fatal(err)
	}
	ws := filepath.Join(*dataDir, "workspace")
	_ = os.MkdirAll(ws, 0o755)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Executable: *browserPath})
	if err != nil {
		log.Fatal(err)
	}
	defer browser.Close()
	// Warm-up run: the GUI remembers "resume last session" from it, and the
	// OS file cache is warm for the measured runs like a normal restart.
	var all []sample
	var requests string
	for i := 0; i <= *runs; i++ {
		s, reqs, err := once(ctx, browser, abs, *dataDir, ws, i == 0)
		if err != nil {
			log.Fatalf("run %d: %v", i, err)
		}
		if i > 0 {
			all = append(all, s)
			requests = reqs
		}
	}
	med := func(get func(sample) time.Duration) time.Duration {
		v := make([]time.Duration, len(all))
		for i, s := range all {
			v[i] = get(s)
		}
		sort.Slice(v, func(a, b int) bool { return v[a] < v[b] })
		return v[len(v)/2].Round(time.Millisecond)
	}
	fmt.Printf("median of %d runs, data %s\n", len(all), *dataDir)
	fmt.Printf("  process start -> HTTP answers:        %s\n", med(func(s sample) time.Duration { return s.HTTP }))
	fmt.Printf("  process start -> page script running: %s\n", med(func(s sample) time.Duration { return s.Script }))
	fmt.Printf("  process start -> session list shown:  %s\n", med(func(s sample) time.Duration { return s.Sessions }))
	fmt.Printf("  process start -> conversation shown:  %s\n", med(func(s sample) time.Duration { return s.History }))
	fmt.Printf("slowest requests of the last run (start..end ms after navigation):\n%s", requests)
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func once(ctx context.Context, browser *testbrowser.Browser, exe, dataDir, ws string, warm bool) (sample, string, error) {
	var s sample
	port := freePort()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	base := "http://" + addr + "/"
	cmd := exec.CommandContext(ctx, exe, "--echo", "--no-window", "--addr", addr, "--data-dir", dataDir, "--home", ws, "--workspace", ws)
	cmd.Dir = filepath.Dir(exe)
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return s, "", err
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	for {
		resp, err := http.Get(base + "api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Since(start) > time.Minute {
			return s, "", fmt.Errorf("server did not answer")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.HTTP = time.Since(start)
	page, err := browser.NewPage(ctx, base)
	if err != nil {
		return s, "", err
	}
	navigated := time.Now()
	if warm {
		// Turn on "resume the last conversation", as a user with history has.
		var ok bool
		for time.Since(navigated) < 30*time.Second {
			if page.Eval(ctx, `typeof ui!=='undefined'&&typeof saveUI==='function'&&!!document.querySelector('#session-list .side-item')`, &ok) == nil && ok {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = page.Eval(ctx, `ui.resumeLastSession=true;saveUI();true`, &ok)
		time.Sleep(1500 * time.Millisecond) // settings save is debounced
		return s, "", nil
	}
	var state struct{ Script, Sessions, History bool }
	for {
		_ = page.Eval(ctx, `({Script: typeof ui!=='undefined', Sessions: !!document.querySelector('#session-list .side-item'), History: document.querySelectorAll('#stream .msg-assistant, #stream .msg-user').length>0})`, &state)
		now := time.Since(start)
		if state.Script && s.Script == 0 {
			s.Script = now
		}
		if state.Sessions && s.Sessions == 0 {
			s.Sessions = now
		}
		if state.History && s.History == 0 {
			s.History = now
		}
		if s.Sessions > 0 && s.History > 0 {
			break
		}
		if time.Since(navigated) > time.Minute {
			return s, "", fmt.Errorf("interface not ready: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var entries []struct {
		Name  string  `json:"name"`
		Start float64 `json:"startTime"`
		Dur   float64 `json:"duration"`
		Size  float64 `json:"transferSize"`
	}
	_ = page.Eval(ctx, `performance.getEntriesByType('resource').map(e=>({name:e.name,startTime:e.startTime,duration:e.duration,transferSize:e.transferSize}))`, &entries)
	sort.Slice(entries, func(a, b int) bool { return entries[a].Dur > entries[b].Dur })
	var b strings.Builder
	for i, e := range entries {
		if i == 12 {
			break
		}
		name := strings.TrimPrefix(e.Name, strings.TrimSuffix(base, "/"))
		fmt.Fprintf(&b, "  %6.0f..%6.0f  %7.0f B  %s\n", e.Start, e.Start+e.Dur, e.Size, name)
	}
	raw, _ := json.Marshal(state)
	_ = raw
	return s, b.String(), nil
}
