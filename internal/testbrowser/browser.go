package testbrowser

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Options selects a development browser, never an existing browser session.
// Executable defaults to SUPERCLI_TEST_BROWSER, PATH, then standard installs.
type Options struct {
	Executable    string
	Width, Height int
}

type Browser struct {
	cmd        *exec.Cmd
	profileDir string
	width      int
	height     int
	cdp        *cdpClient
	waitDone   chan struct{}
	waitErr    error // published by closing waitDone
	scanDone   chan struct{}
	output     *os.File
	logMu      sync.Mutex
	logTail    string
	closeOnce  sync.Once
	closeDone  chan struct{}
	closeErr   error
}

type Page struct {
	browser *Browser
	session string
	navMu   sync.Mutex
}

func findExecutable(explicit string) (string, error) {
	selected := explicit
	if selected == "" {
		selected = os.Getenv("SUPERCLI_TEST_BROWSER")
	}
	if selected != "" {
		path, err := exec.LookPath(selected)
		if err != nil {
			return "", fmt.Errorf("dev-only test browser %q: %w; install Chrome/Chromium/Edge and set SUPERCLI_TEST_BROWSER or -browser to its executable", selected, err)
		}
		return path, nil
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA"), `C:\Program Files`, `C:\Program Files (x86)`} {
			if root == "" {
				continue
			}
			for _, relative := range []string{`Google\Chrome\Application\chrome.exe`, `Microsoft\Edge\Application\msedge.exe`} {
				candidate := filepath.Join(root, relative)
				if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
					return candidate, nil
				}
			}
		}
	}
	return "", errors.New("no dev-only test browser found: install Chrome/Chromium/Edge and set SUPERCLI_TEST_BROWSER or pass -browser with its executable (no existing browser profile is used)")
}

func viewportSize(width, height int) error {
	if width < 1 || height < 1 || width > 16384 || height > 16384 {
		return fmt.Errorf("invalid viewport %dx%d (each dimension must be 1..16384)", width, height)
	}
	return nil
}

// Start launches a headless process with its own temporary profile. Readiness is
// the DevTools line on a single merged stdout/stderr pipe, never port polling.
// The context bounds startup and also closes the owned browser on cancellation.
func Start(ctx context.Context, opts Options) (*Browser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.Width == 0 {
		opts.Width = 1280
	}
	if opts.Height == 0 {
		opts.Height = 900
	}
	if err := viewportSize(opts.Width, opts.Height); err != nil {
		return nil, err
	}
	executable, err := findExecutable(opts.Executable)
	if err != nil {
		return nil, err
	}
	profile, err := os.MkdirTemp("", "supercli-testbrowser-*")
	if err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = os.RemoveAll(profile)
		return nil, err
	}
	args := []string{
		"--headless", "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0",
		"--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check",
		"--disable-background-networking", "--disable-component-update", "--disable-default-apps",
		"--disable-sync", "--disable-extensions", "--disable-background-timer-throttling",
		"--disable-backgrounding-occluded-windows", "--disable-renderer-backgrounding",
		"--js-flags=--expose-gc", "--enable-precise-memory-info",
		fmt.Sprintf("--window-size=%d,%d", opts.Width, opts.Height), "about:blank",
	}
	b := &Browser{cmd: exec.Command(executable, args...), profileDir: profile, width: opts.Width, height: opts.Height,
		waitDone: make(chan struct{}), scanDone: make(chan struct{}), closeDone: make(chan struct{}), output: reader}
	b.cmd.Stdout, b.cmd.Stderr = writer, writer
	if err := b.cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		_ = os.RemoveAll(profile)
		return nil, fmt.Errorf("start dev-only test browser: %w", err)
	}
	writer.Close()
	ready := make(chan string, 1)
	go func() {
		defer close(b.scanDone)
		defer reader.Close()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 256<<10)
		for scanner.Scan() {
			line := scanner.Text()
			b.appendLog(line)
			if endpoint, ok := strings.CutPrefix(strings.TrimSpace(line), "DevTools listening on "); ok {
				select {
				case ready <- strings.TrimSpace(endpoint):
				default:
				}
			}
		}
		if err := scanner.Err(); err != nil {
			b.appendLog("browser output scanner: " + err.Error())
		}
	}()
	go func() {
		b.waitErr = b.cmd.Wait()
		close(b.waitDone)
	}()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	fail := func(err error) (*Browser, error) {
		closeErr := b.Close()
		return nil, errors.Join(fmt.Errorf("%w\n%s", err, b.diagnostics()), closeErr)
	}
	select {
	case <-startup.Done():
		return fail(fmt.Errorf("waiting for DevTools listening line: %w", startup.Err()))
	case <-b.waitDone:
		return fail(fmt.Errorf("test browser exited before CDP readiness: %v", b.waitErr))
	case <-b.scanDone:
		return fail(errors.New("test browser closed its output before CDP readiness"))
	case endpoint := <-ready:
		ws, err := dialWebSocket(startup, endpoint)
		if err != nil {
			return fail(fmt.Errorf("connect to browser CDP: %w", err))
		}
		b.cdp = newCDP(ws)
	}
	var version struct{ Product string }
	if err := b.cdp.call(startup, "", "Browser.getVersion", nil, &version); err != nil {
		return fail(err)
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = b.Close()
		case <-b.closeDone:
		}
	}()
	return b, nil
}

func (b *Browser) appendLog(line string) {
	b.logMu.Lock()
	defer b.logMu.Unlock()
	b.logTail += line + "\n"
	if len(b.logTail) > 8192 {
		b.logTail = b.logTail[len(b.logTail)-8192:]
	}
}

func (b *Browser) diagnostics() string {
	b.logMu.Lock()
	defer b.logMu.Unlock()
	return b.logTail
}

func (b *Browser) NewPage(ctx context.Context, address string) (*Page, error) {
	var target struct{ TargetID string }
	if err := b.cdp.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "background": true}, &target); err != nil {
		return nil, err
	}
	var attached struct{ SessionID string }
	if err := b.cdp.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached); err != nil {
		return nil, err
	}
	page := &Page{browser: b, session: attached.SessionID}
	for _, command := range []struct {
		method string
		params any
	}{{"Page.enable", nil}, {"Page.setLifecycleEventsEnabled", map[string]bool{"enabled": true}}} {
		if err := b.cdp.call(ctx, page.session, command.method, command.params, nil); err != nil {
			return nil, err
		}
	}
	if err := page.SetViewport(ctx, b.width, b.height); err != nil {
		return nil, err
	}
	if err := page.Navigate(ctx, address); err != nil {
		return nil, err
	}
	return page, nil
}

func (p *Page) Navigate(ctx context.Context, address string) error {
	if _, err := url.ParseRequestURI(address); err != nil {
		return fmt.Errorf("navigation URL: %w", err)
	}
	p.navMu.Lock()
	defer p.navMu.Unlock()
	mark := p.browser.cdp.eventMark()
	var result struct {
		LoaderID   string `json:"loaderId"`
		ErrorText  string `json:"errorText"`
		IsDownload bool   `json:"isDownload"`
	}
	if err := p.browser.cdp.call(ctx, p.session, "Page.navigate", map[string]string{"url": address}, &result); err != nil {
		return err
	}
	if result.ErrorText != "" || result.IsDownload {
		return fmt.Errorf("Page.navigate %q: %s (download=%v)", address, result.ErrorText, result.IsDownload)
	}
	if result.LoaderID == "" { // same-document navigation has no new load event
		return nil
	}
	// Match the loader, not an old about:blank load. Both lifecycle and
	// loadEventFired are buffered by the reader before navigate replies arrive.
	if err := p.browser.cdp.waitEvent(ctx, mark, func(event cdpMessage) bool {
		if event.SessionID != p.session || event.Method != "Page.lifecycleEvent" {
			return false
		}
		var load struct{ Name, LoaderID string }
		return json.Unmarshal(event.Params, &load) == nil && load.Name == "load" && load.LoaderID == result.LoaderID
	}); err != nil {
		return fmt.Errorf("Page.navigate %q load: %w", address, err)
	}
	// Page.loadEventFired may already be queued, even before Page.navigate's
	// reply. Waiting through the retained events avoids losing that signal.
	if err := p.browser.cdp.waitEvent(ctx, mark, func(event cdpMessage) bool {
		return event.SessionID == p.session && event.Method == "Page.loadEventFired"
	}); err != nil {
		return fmt.Errorf("Page.navigate %q loadEventFired: %w", address, err)
	}
	return nil
}

func (p *Page) SetViewport(ctx context.Context, width, height int) error {
	if err := viewportSize(width, height); err != nil {
		return err
	}
	return p.browser.cdp.call(ctx, p.session, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false,
	}, nil)
}

type jsStack struct {
	CallFrames []struct {
		FunctionName string `json:"functionName"`
		URL          string `json:"url"`
		LineNumber   int    `json:"lineNumber"`
		ColumnNumber int    `json:"columnNumber"`
	} `json:"callFrames"`
	Parent *jsStack `json:"parent"`
}

type jsException struct {
	Text      string `json:"text"`
	Exception struct {
		Description string          `json:"description"`
		Value       json.RawMessage `json:"value"`
	} `json:"exception"`
	StackTrace *jsStack `json:"stackTrace"`
}

func (e *jsException) Error() string {
	var text strings.Builder
	fmt.Fprintf(&text, "Runtime.evaluate: %s", e.Text)
	if e.Exception.Description != "" {
		fmt.Fprintf(&text, "\n%s", e.Exception.Description)
	} else if len(e.Exception.Value) != 0 {
		fmt.Fprintf(&text, "\n%s", e.Exception.Value)
	}
	for stack := e.StackTrace; stack != nil; stack = stack.Parent {
		for _, frame := range stack.CallFrames {
			fmt.Fprintf(&text, "\n    at %s (%s:%d:%d)", frame.FunctionName, frame.URL, frame.LineNumber+1, frame.ColumnNumber+1)
		}
	}
	return text.String()
}

func (p *Page) Eval(ctx context.Context, expression string, out any) error {
	var result struct {
		Result struct {
			Type                string          `json:"type"`
			Value               json.RawMessage `json:"value"`
			UnserializableValue string          `json:"unserializableValue"`
		} `json:"result"`
		ExceptionDetails *jsException `json:"exceptionDetails"`
	}
	if err := p.browser.cdp.call(ctx, p.session, "Runtime.evaluate", map[string]any{
		"expression": expression, "awaitPromise": true, "returnByValue": true,
	}, &result); err != nil {
		return err
	}
	if result.ExceptionDetails != nil {
		return result.ExceptionDetails
	}
	if out == nil {
		return nil
	}
	if len(result.Result.Value) == 0 {
		return fmt.Errorf("Runtime.evaluate returned non-JSON %s %s", result.Result.Type, result.Result.UnserializableValue)
	}
	if err := json.Unmarshal(result.Result.Value, out); err != nil {
		return fmt.Errorf("Runtime.evaluate value: %w", err)
	}
	return nil
}

func (p *Page) Screenshot(ctx context.Context) ([]byte, error) {
	var result struct{ Data string }
	if err := p.browser.cdp.call(ctx, p.session, "Page.captureScreenshot", map[string]any{"format": "png", "fromSurface": true}, &result); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	return data, nil
}

// Close first asks our browser to exit, waits, and kills only our own process if
// necessary. It removes only the unique profile created by Start, never a user
// profile. It is safe to call more than once, including after cancellation.
func (b *Browser) Close() error {
	b.closeOnce.Do(func() {
		defer close(b.closeDone)
		var errs []error
		if b.cdp != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = b.cdp.call(ctx, "", "Browser.close", nil, nil)
			cancel()
		}
		exited := false
		if b.cdp == nil {
			// Startup failed before a CDP connection existed.
			_ = b.cmd.Process.Kill()
		} else {
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-b.waitDone:
				exited = true
			case <-timer.C:
				errs = append(errs, errors.New("browser did not exit after Browser.close; killing owned process"))
				_ = b.cmd.Process.Kill()
			}
			timer.Stop()
		}
		if !exited {
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-b.waitDone:
				exited = true
			case <-timer.C:
				errs = append(errs, errors.New("owned browser did not exit after kill"))
			}
			timer.Stop()
		}
		if b.cdp != nil {
			b.cdp.shutdown(nil)
		}
		if exited {
			// A child inheriting the output handle must not make Close hang
			// after the browser process itself has already exited.
			timer := time.NewTimer(time.Second)
			select {
			case <-b.scanDone:
			case <-timer.C:
				_ = b.output.Close()
				<-b.scanDone
			}
			timer.Stop()
			if b.waitErr != nil && b.cdp != nil {
				errs = append(errs, fmt.Errorf("browser exit: %w", b.waitErr))
			}
			if err := os.RemoveAll(b.profileDir); err != nil {
				errs = append(errs, fmt.Errorf("remove owned test browser profile: %w", err))
			}
		}
		b.closeErr = errors.Join(errs...)
	})
	return b.closeErr
}
