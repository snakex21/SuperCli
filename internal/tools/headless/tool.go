// Package headless controls explicitly selected local automation endpoints.
// No app is started, scanned, or connected while its tool is dormant.
package headless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"supercli/internal/tools/core"
	"supercli/internal/tools/media"
	"sync"
	"time"
)

type Tool struct{ BaseDir, DataDir string }

func New(baseDir, dataDir string) *Tool { return &Tool{BaseDir: baseDir, DataDir: dataDir} }
func (t *Tool) Spec() core.Tool {
	return core.Tool{Name: "headless_control", Description: "Control an explicitly selected local headless QEMU (qmp) or browser (webdriver). Start QEMU/driver once with process_session; reuse endpoint/session. QMP: status, keys, click, screenshot, wait_event. WebDriver: open headless browser, status, inspect semantic DOM, navigate, click/type CSS selector, screenshot, close. Prefer inspect/status over image inference. Chat preview is automatic; attach=true only for pixel analysis. Does not change host mouse/keyboard or foreground window.",
		Schema: `{"type":"object","properties":{"protocol":{"type":"string","enum":["qmp","webdriver"]},"endpoint":{"type":"string","description":"Explicit loopback endpoint: tcp://127.0.0.1:4444 QMP or http://127.0.0.1:9515 WebDriver"},"action":{"type":"string","enum":["open","status","inspect","navigate","keys","click","type","screenshot","wait_event","close"]},"session_id":{"type":"string"},"browser":{"type":"string","enum":["chrome","firefox"],"default":"chrome"},"binary":{"type":"string","description":"Optional browser executable"},"url":{"type":"string"},"selector":{"type":"string","description":"WebDriver CSS selector"},"text":{"type":"string","maxLength":4096},"keys":{"type":"array","items":{"type":"string"},"maxItems":16,"description":"QMP qcode chord, e.g. [ctrl,alt,delete]"},"x":{"type":"integer","minimum":0,"maximum":32767},"y":{"type":"integer","minimum":0,"maximum":32767},"button":{"type":"string","enum":["left","right","middle"],"default":"left"},"event":{"type":"string","description":"QMP event to await without polling, e.g. SHUTDOWN"},"attach":{"type":"boolean","default":false},"image_detail":{"type":"string","enum":["auto","original"],"default":"auto"},"timeout_ms":{"type":"integer","minimum":1000,"maximum":300000,"default":30000}},"required":["protocol","endpoint","action"]}`, Fn: t.Execute}
}

type params struct {
	Protocol    string   `json:"protocol"`
	Action      string   `json:"action"`
	Endpoint    string   `json:"endpoint"`
	SessionID   string   `json:"session_id"`
	Browser     string   `json:"browser"`
	Binary      string   `json:"binary"`
	URL         string   `json:"url"`
	Selector    string   `json:"selector"`
	Text        string   `json:"text"`
	Keys        []string `json:"keys"`
	X           int      `json:"x"`
	Y           int      `json:"y"`
	Button      string   `json:"button"`
	Attach      bool     `json:"attach"`
	ImageDetail string   `json:"image_detail"`
	TimeoutMS   int      `json:"timeout_ms"`
	Event       string   `json:"event"`
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (core.Result, error) {
	if len(raw) > 64<<10 {
		return core.Result{Err: fmt.Errorf("headless_control: arguments exceed 64 KiB")}, nil
	}
	var p params
	if err := json.Unmarshal(raw, &p); err != nil {
		return core.Result{Err: fmt.Errorf("headless_control: invalid arguments: %w", err)}, nil
	}
	if p.Protocol == "qmp" && p.Action == "click" {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if len(fields["x"]) == 0 || len(fields["y"]) == 0 || string(fields["x"]) == "null" || string(fields["y"]) == "null" {
			return core.Result{Err: fmt.Errorf("QMP click requires x and y absolute coordinates")}, nil
		}
	}
	if p.Protocol != "qmp" && p.Protocol != "webdriver" {
		return core.Result{Err: fmt.Errorf("headless_control: protocol must be qmp or webdriver")}, nil
	}
	if p.ImageDetail != "" && p.ImageDetail != "auto" && p.ImageDetail != "original" {
		return core.Result{Err: fmt.Errorf("headless_control: image_detail must be auto or original")}, nil
	}
	duration := 30 * time.Second
	if p.TimeoutMS != 0 {
		if p.TimeoutMS < 1000 || p.TimeoutMS > 300000 {
			return core.Result{Err: fmt.Errorf("headless_control: timeout_ms must be 1000..300000")}, nil
		}
		duration = time.Duration(p.TimeoutMS) * time.Millisecond
	}
	scheme := "tcp"
	if p.Protocol == "webdriver" {
		scheme = "http"
	}
	endpoint, err := localEndpoint(p.Endpoint, scheme)
	if err != nil {
		return core.Result{Err: err}, nil
	}
	p.Endpoint = endpoint.String()
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	release, err := lockTarget(ctx, p.Protocol+":"+p.Endpoint)
	if err != nil {
		return core.Result{Err: err}, nil
	}
	defer release()
	var result core.Result
	if p.Protocol == "qmp" {
		result, err = t.runQMP(ctx, p)
	} else {
		result, err = t.runWebDriver(ctx, p)
	}
	if err != nil {
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		return core.Result{Err: fmt.Errorf("headless_control: %w", err)}, nil
	}
	return result, nil
}

// Literal loopback addresses avoid DNS/proxy redirection of local controls.
func localEndpoint(raw, scheme string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = scheme + "://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != scheme || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("headless_control: expected a local %s endpoint", scheme)
	}
	ip := net.ParseIP(u.Hostname())
	port, e := strconv.Atoi(u.Port())
	if ip == nil || !ip.IsLoopback() || e != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("headless_control: endpoint requires a literal loopback IP and port")
	}
	if scheme == "tcp" && u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("headless_control: QMP endpoint cannot contain a path")
	}
	u.Host = net.JoinHostPort(ip.String(), strconv.Itoa(port))
	u.RawPath = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}
func jsonResult(value any) (core.Result, error) {
	data, err := json.Marshal(value)
	return core.Result{Text: string(data)}, err
}
func capturedResult(ctx context.Context, dataDir, source string, data []byte, attach bool, detail string) (core.Result, error) {
	return media.PreviewCapturedImage(ctx, dataDir, source, data, attach, detail)
}

type targetGate struct {
	ready chan struct{}
	users int
}

var gates struct {
	sync.Mutex
	targets map[string]*targetGate
}

func lockTarget(ctx context.Context, key string) (func(), error) {
	gates.Lock()
	if gates.targets == nil {
		gates.targets = make(map[string]*targetGate)
	}
	gate := gates.targets[key]
	if gate == nil {
		if len(gates.targets) >= 16 {
			gates.Unlock()
			return nil, fmt.Errorf("headless_control: 16 active endpoints; wait for a control operation to finish")
		}
		gate = &targetGate{ready: make(chan struct{}, 1)}
		gates.targets[key] = gate
	}
	gate.users++
	gates.Unlock()
	drop := func() {
		gates.Lock()
		gate.users--
		if gate.users == 0 {
			delete(gates.targets, key)
		}
		gates.Unlock()
	}
	select {
	case gate.ready <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate.ready
			drop()
			return nil, err
		}
		return func() { <-gate.ready; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}
