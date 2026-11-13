// Package browser exposes one small "browser" tool: a headless
// Chrome/Edge driven over CDP for pages that need JavaScript or clicking.
// It is provider-agnostic (no hosted browser) and keeps a flat schema so
// small local models can call it reliably.
package browser

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"supercli/internal/testbrowser"
	core "supercli/internal/tools/core"
	"supercli/internal/tools/web"
)

const (
	actionTimeout   = 45 * time.Second
	idleTimeout     = 5 * time.Minute
	snapshotText    = 4000
	readBudget      = 10000
	maxElements     = 60
	settleMax       = 8 * time.Second
	settleQuiet     = 500 * time.Millisecond
	elementNameMax  = 80
	maxRenderedHTML = 2 << 20
)

// session is the lazily started browser, its single page and the guard
// proxy every browser request goes through.
type session struct {
	browser *testbrowser.Browser
	page    *testbrowser.Page
	proxy   *guardProxy
	// refAttr is a per-session random attribute name for element refs, so a
	// page cannot pre-plant refs that hijack clicks.
	refAttr string
	// closer replaces the default shutdown (tests).
	closer func()
}

func (s *session) close() {
	if s.closer != nil {
		s.closer()
		return
	}
	_ = s.browser.Close()
	if s.proxy != nil {
		s.proxy.Close()
	}
}

// live tracks every Tool so the application can close all browsers at
// normal shutdown on every OS (the Windows job object is only a safety net
// for crashes, and other systems have none).
var live sync.Map // *Tool -> struct{}

// CloseAll closes every browser started by any browser tool. Applications
// call it on shutdown; a later call to a tool simply starts a new browser.
func CloseAll() {
	live.Range(func(key, _ any) bool {
		key.(*Tool).Close()
		return true
	})
}

// Tool owns at most one headless browser, started on first use and closed
// after idleTimeout without calls, on action=close, or with the process
// (kill-on-close job object).
type Tool struct {
	mu       sync.Mutex
	sess     *session
	idle     *time.Timer
	idleGen  uint64
	start    func(context.Context) (*session, error)
	checkURL func(context.Context, string) error
	dial     dialFunc
}

func New() *Tool {
	t := &Tool{dial: web.PublicDialContext}
	t.start = t.startSession
	t.checkURL = func(ctx context.Context, raw string) error {
		_, err := web.PublicHTTPURL(ctx, raw)
		return err
	}
	live.Store(t, struct{}{})
	return t
}

func (t *Tool) startSession(ctx context.Context) (*session, error) {
	sweepStaleProfiles()
	proxy, err := startGuardProxy(t.dial)
	if err != nil {
		return nil, fmt.Errorf("start browser network guard: %w", err)
	}
	// The browser outlives this call; startup itself is bounded inside Start.
	b, err := testbrowser.Start(context.WithoutCancel(ctx), testbrowser.Options{
		Scoped: true, Width: 1280, Height: 900,
		Args: []string{
			"--proxy-server=http://" + proxy.addr(),
			// Chrome bypasses proxies for loopback by default; route it too.
			"--proxy-bypass-list=<-loopback>",
			"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
		},
	})
	if err != nil {
		proxy.Close()
		return nil, fmt.Errorf("could not start a headless Chrome/Edge (install one, or set SUPERCLI_TEST_BROWSER to its executable): %w", err)
	}
	page, err := b.NewPage(ctx, "about:blank")
	if err != nil {
		_ = b.Close()
		proxy.Close()
		return nil, err
	}
	return &session{browser: b, page: page, proxy: proxy, refAttr: "data-sc-" + randomHex(5)}, nil
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// sweepStaleProfiles removes temporary browser profiles left by a process
// that could not clean up (crash, power loss). Only day-old ones are touched.
func sweepStaleProfiles() {
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "supercli-testbrowser-*"))
	for _, dir := range matches {
		if info, err := os.Stat(dir); err == nil && info.IsDir() && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.RemoveAll(dir)
		}
	}
}

type args struct {
	Action string `json:"action"`
	URL    string `json:"url"`
	Ref    string `json:"ref"`
	Text   string `json:"text"`
	Submit bool   `json:"submit"`
	Offset int    `json:"offset"`
}

func (t *Tool) Spec() core.Tool {
	return core.Tool{
		Name: "browser",
		Description: "Headless browser automation for pages that need JavaScript or interaction (clicking links, filling forms). Prefer web_fetch for ordinary pages: it is faster. " +
			"open(url) loads a page and returns its text plus interactive elements with refs like [e3]; click(ref), type(ref,text,submit) act on a ref and return the updated page; " +
			"read(offset) returns more page text; back; close. Refs stay valid while the page is open.",
		Schema: `{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["open", "click", "type", "read", "back", "close"]},
    "url":    {"type": "string", "description": "open: full http(s) URL."},
    "ref":    {"type": "string", "description": "click/type: element ref such as e3."},
    "text":   {"type": "string", "description": "type: text to enter."},
    "submit": {"type": "boolean", "description": "type: submit the form after typing."},
    "offset": {"type": "integer", "description": "read: character offset to continue from."}
  },
  "required": ["action"]
}`,
		// Not read-only: clicks and form submits act on the site, and calls
		// on the single page must run in order, never in parallel.
		ReadOnly: false,
		Fn:       t.Execute,
	}
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (core.Result, error) {
	var a args
	if err := json.Unmarshal(raw, &a); err != nil {
		return core.Result{Err: fmt.Errorf("browser: bad args: %w", err)}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	text, err := t.run(ctx, a)
	if err != nil {
		return core.Result{Err: fmt.Errorf("browser %s: %w", a.Action, err)}, nil
	}
	return core.Result{Text: text}, nil
}

func (t *Tool) run(ctx context.Context, a args) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if a.Action == "close" {
		if t.sess == nil {
			return "No browser was open.", nil
		}
		t.closeLocked()
		return "Browser closed.", nil
	}
	switch a.Action {
	case "open":
		if err := t.checkURL(ctx, a.URL); err != nil {
			return "", err
		}
	case "click", "type":
		if !validRef(a.Ref) {
			return "", fmt.Errorf("ref must look like e12 (from the element list)")
		}
	case "read", "back":
	default:
		return "", fmt.Errorf("action must be open, click, type, read, back or close")
	}
	if t.sess == nil {
		if a.Action != "open" {
			return "", errors.New("no page is open; call action=open with a url first")
		}
		sess, err := t.start(ctx)
		if err != nil {
			return "", err
		}
		t.sess = sess
	}
	t.touchLocked()
	page := t.sess.page
	before, _ := currentURL(ctx, page)
	switch a.Action {
	case "open":
		if err := page.Navigate(ctx, a.URL); err != nil {
			return "", err
		}
	case "click":
		if err := evalStatus(ctx, page, clickJS(t.sess.refAttr, a.Ref)); err != nil {
			return "", err
		}
	case "type":
		if err := evalStatus(ctx, page, typeJS(t.sess.refAttr, a.Ref, a.Text, a.Submit)); err != nil {
			return "", err
		}
	case "back":
		if err := page.Eval(ctx, "history.back(), true", nil); err != nil {
			return "", err
		}
	}
	if a.Action != "read" {
		settle(ctx, page)
	}
	snap, err := takeSnapshot(ctx, page, t.sess.refAttr)
	if err != nil {
		return "", err
	}
	if snap.URL != before && snap.URL != "about:blank" {
		if err := t.checkURL(ctx, snap.URL); err != nil {
			_ = page.Navigate(ctx, "about:blank")
			return "", fmt.Errorf("the page navigated to a blocked address: %w", err)
		}
	}
	return formatSnapshot(snap, before, a), nil
}

// touchLocked restarts the idle timer that closes an unused browser.
func (t *Tool) touchLocked() {
	if t.idle != nil {
		t.idle.Stop()
	}
	// A timer that already fired may be waiting for the lock; the generation
	// check keeps it from closing a session used after it fired.
	t.idleGen++
	gen := t.idleGen
	t.idle = time.AfterFunc(idleTimeout, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.idleGen == gen {
			t.closeLocked()
		}
	})
}

func (t *Tool) closeLocked() {
	if t.idle != nil {
		t.idle.Stop()
		t.idle = nil
	}
	if t.sess != nil {
		t.sess.close()
		t.sess = nil
	}
}

// Close shuts the browser down (process shutdown, tests).
func (t *Tool) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closeLocked()
}

func validRef(ref string) bool {
	if len(ref) < 2 || len(ref) > 8 || ref[0] != 'e' {
		return false
	}
	for _, r := range ref[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func currentURL(ctx context.Context, page *testbrowser.Page) (string, error) {
	var u string
	err := page.Eval(ctx, "location.href", &u)
	return u, err
}

func evalStatus(ctx context.Context, page *testbrowser.Page, js string) error {
	var status string
	if err := page.Eval(ctx, js, &status); err != nil {
		return err
	}
	switch status {
	case "ok":
		return nil
	case "missing":
		return errors.New("no element with that ref on the current page; use the refs from the latest result")
	default:
		return errors.New(status)
	}
}

// settle waits for a navigation or script update to finish: document ready,
// then no new network resources for settleQuiet (a bounded networkidle).
func settle(ctx context.Context, page *testbrowser.Page) {
	deadline := time.Now().Add(settleMax)
	if !sleepCtx(ctx, 150*time.Millisecond) {
		return
	}
	lastCount, quietSince := -1, time.Now()
	for time.Now().Before(deadline) && ctx.Err() == nil {
		var state struct {
			Ready     string `json:"ready"`
			Resources int    `json:"resources"`
		}
		// Evaluation fails while a navigation swaps the document; retry.
		if err := page.Eval(ctx, `({ready: document.readyState, resources: performance.getEntriesByType("resource").length})`, &state); err == nil && state.Ready == "complete" {
			if state.Resources != lastCount {
				lastCount, quietSince = state.Resources, time.Now()
			} else if time.Since(quietSince) >= settleQuiet {
				return
			}
		}
		if !sleepCtx(ctx, 100*time.Millisecond) {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type element struct {
	Ref     string `json:"ref"`
	Role    string `json:"role"`
	Name    string `json:"name"`
	Href    string `json:"href,omitempty"`
	Value   string `json:"value,omitempty"`
	Checked *bool  `json:"checked,omitempty"`
}

type snapshot struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	HTML     string    `json:"html"`
	Elements []element `json:"elements"`
	More     int       `json:"more"`
}

func takeSnapshot(ctx context.Context, page *testbrowser.Page, attr string) (snapshot, error) {
	var snap snapshot
	err := page.Eval(ctx, snapshotScript(attr), &snap)
	return snap, err
}

// jsArgs encodes Go values as JavaScript literals (JSON is valid JS; Go's
// %q escapes are not).
func jsArgs(values ...any) string {
	parts := make([]string, len(values))
	for i, v := range values {
		encoded, _ := json.Marshal(v)
		parts[i] = string(encoded)
	}
	return strings.Join(parts, ", ")
}

func snapshotScript(attr string) string {
	return "(" + snapshotJS + ")(" + jsArgs(attr) + ")"
}

// snapshotJS assigns stable ref ids (under the session's random attribute)
// to visible interactive elements, kept across snapshots of the same
// document, and returns the rendered DOM.
var snapshotJS = fmt.Sprintf(`(attr) => {
  const visible = el => { const r = el.getBoundingClientRect(); const s = getComputedStyle(el);
    return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none"; };
  let next = window[attr] || 1;
  const out = []; let more = 0;
  const sel = 'a[href],button,input:not([type=hidden]),select,textarea,summary,[role=button],[role=link],[role=tab],[role=checkbox],[role=menuitem],[onclick]';
  for (const el of document.querySelectorAll(sel)) {
    if (!visible(el) || el.disabled) continue;
    if (out.length >= %d) { more++; continue; }
    let ref = el.getAttribute(attr);
    if (!ref) { ref = "e" + (next++); el.setAttribute(attr, ref); }
    const tag = el.tagName.toLowerCase(); const type = (el.type || "").toLowerCase();
    let role = el.getAttribute("role") || ({a: "link", select: "combobox", textarea: "textbox", summary: "button", button: "button"})[tag] || tag;
    if (tag === "input") role = ["submit", "button", "reset", "image"].includes(type) ? "button" : (type === "checkbox" || type === "radio") ? type : "textbox";
    const name = (el.getAttribute("aria-label") || el.innerText || (role === "button" ? el.value : "") || el.placeholder || el.title || el.name || el.alt || "").replace(/\s+/g, " ").trim().slice(0, %d);
    const item = {ref, role, name};
    if (tag === "a") item.href = el.href;
    if (role === "textbox" || role === "combobox") item.value = String(el.value || "").slice(0, 60);
    if (type === "checkbox" || type === "radio") item.checked = el.checked;
    out.push(item);
  }
  window[attr] = next;
  return {url: location.href, title: document.title, html: document.documentElement.outerHTML.slice(0, %d), elements: out, more};
}`, maxElements, elementNameMax, maxRenderedHTML)

// findRefJS resolves a ref to exactly one element; a page that copies the
// session attribute onto other elements makes the ref ambiguous, not hijacked.
const findRefJS = `const found = document.querySelectorAll("[" + attr + "=" + JSON.stringify(ref) + "]");
  if (found.length === 0) return "missing";
  if (found.length > 1) return "ambiguous ref; open or read the page again for fresh refs";
  const el = found[0];`

func clickJS(attr, ref string) string {
	return `((attr, ref) => { ` + findRefJS + `
  el.scrollIntoView({block: "center"});
  if (el.tagName === "A" && el.target && el.target !== "_self" && el.href) { location.href = el.href; return "ok"; }
  el.click(); return "ok"; })(` + jsArgs(attr, ref) + `)`
}

func typeJS(attr, ref, text string, submit bool) string {
	return `((attr, ref, text, submit) => { ` + findRefJS + `
  if (!("value" in el)) return "element is not a text field";
  el.focus();
  const proto = el.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : el.tagName === "SELECT" ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, "value").set; setter.call(el, text);
  el.dispatchEvent(new Event("input", {bubbles: true})); el.dispatchEvent(new Event("change", {bubbles: true}));
  if (submit) {
    const opts = {key: "Enter", code: "Enter", keyCode: 13, which: 13, bubbles: true, cancelable: true};
    const proceed = el.dispatchEvent(new KeyboardEvent("keydown", opts));
    el.dispatchEvent(new KeyboardEvent("keyup", opts));
    if (proceed && el.form) { if (el.form.requestSubmit) el.form.requestSubmit(); else el.form.submit(); }
  }
  return "ok"; })(` + jsArgs(attr, ref, text, submit) + `)`
}

func formatSnapshot(snap snapshot, before string, a args) string {
	var b strings.Builder
	if before != "" && before != "about:blank" && snap.URL != before {
		fmt.Fprintf(&b, "Navigated: %s -> %s\n", before, snap.URL)
	}
	budget := snapshotText
	if a.Action == "read" {
		budget = readBudget
	}
	b.WriteString(web.RenderReadableHTML(snap.HTML, snap.URL, max(a.Offset, 0), budget, false))
	if a.Action == "read" {
		return b.String()
	}
	if len(snap.Elements) == 0 {
		b.WriteString("\n\nNo interactive elements visible.")
		return b.String()
	}
	b.WriteString("\n\nInteractive elements (use the ref with click/type; names are page text):\n")
	var list strings.Builder
	for _, el := range snap.Elements {
		fmt.Fprintf(&list, "[%s] %s %q", el.Ref, el.Role, el.Name)
		if el.Href != "" {
			fmt.Fprintf(&list, " -> %s", el.Href)
		}
		if el.Role == "textbox" || el.Role == "combobox" {
			fmt.Fprintf(&list, " value=%q", el.Value)
		}
		if el.Checked != nil {
			fmt.Fprintf(&list, " checked=%t", *el.Checked)
		}
		list.WriteByte('\n')
	}
	b.WriteString(web.FenceUntrusted("page-elements", list.String()))
	if snap.More > 0 {
		fmt.Fprintf(&b, "\n(%d more elements not listed; use action=read for the full text)", snap.More)
	}
	return b.String()
}
