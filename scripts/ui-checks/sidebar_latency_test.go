package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// TestSidebarLatencyProfile is a diagnostic, not a speed budget. It exercises
// the complete production document in a disposable native headless profile.
// Run: go test ./scripts/ui-checks -run '^TestSidebarLatencyProfile$' -count=1 -v
// All numbers describe this synthetic HTTP fixture, NOT desktop GUI wall time.
// In particular a frame observation is a paint opportunity, not proof that a
// compositor presented pixels. No assertion requires the old slow path to stay.
func TestSidebarLatencyProfile(t *testing.T) {
	root, err := repoRoot("")
	if err != nil {
		t.Fatal(err)
	}
	var samples []sidebarProfile
	for repeat := range 3 {
		t.Run(fmt.Sprintf("sample-%d", repeat+1), func(t *testing.T) {
			f, err := newFixture(root, "sidebar-latency", "")
			if err != nil {
				t.Fatal(err)
			}
			defer f.close()
			// assetSnapshot (used by newFixture) also supplies generated locale
			// assets. Keep its init-promise hook; replace no production logic.
			f.assets["/"] = []byte(strings.Replace(string(f.assets["/"]), "<head>", "<head><script>"+sidebarLatencyProbe+"</script>", 1))
			mock := &sidebarLatencyFixture{base: f, epoch: time.Now(), values: map[string]string{
				"orchestrator": "default", "task_model": "", "task_reasoning_effort": "medium",
				"advisor_model": "off", "advisor_reasoning_effort": "low", "review_model": "", "review_reasoning_effort": "high",
				"darwin": "default", "darwin_models": "fixture-main", "darwin_pool_size": "3", "darwin_reasoning_effort": "high",
			}}
			server, err := loopbackServer(http.HandlerFunc(mock.serveHTTP))
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			defer server.CloseClientConnections()
			ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
			defer cancel()
			// Constrain discovery of DevToolsActivePort to this owned directory,
			// never scan other profiles or attach to an existing browser.
			browserTemp := t.TempDir()
			for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
				t.Setenv(key, browserTemp)
			}
			browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1600, Height: 1600})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := browser.Close(); err != nil {
					t.Errorf("close owned browser: %v", err)
				}
			}()
			page, err := browser.NewPage(ctx, server.URL+"/__sidebar_prepare")
			if err != nil {
				t.Fatal(err)
			}
			if err := sidebarActivateOwnedTarget(ctx, browserTemp, server.URL+"/__sidebar_prepare"); err != nil {
				t.Fatal(err)
			}
			if err := page.Navigate(ctx, server.URL); err != nil {
				t.Fatal(err)
			}
			const exercise = `(async () => {
  const p = window.__sidebarProbe;
  await window.__uiCheckInit;
  await p.waitStartup();
  for (const name of ['darwin-on','darwin-off','review-on','review-off','orchestrator-on','orchestrator-off']) await p.run(name);
  return p.finish();
})()`
			var sample sidebarProfile
			if err := page.Eval(ctx, exercise, &sample); err != nil {
				var diagnostics any
				_ = page.Eval(ctx, `window.__sidebarProbe.finish()`, &diagnostics)
				t.Fatalf("profile: %v\ndiagnostics=%+v", err, diagnostics)
			}
			if sample.Width < 1420 || sample.Visibility != "visible" || len(sample.Errors) != 0 {
				t.Fatalf("viewport=%d visibility=%s errors=%v", sample.Width, sample.Visibility, sample.Errors)
			}
			if sample.Paints["first-contentful-paint"] <= 0 {
				t.Error("native browser did not emit first-contentful-paint")
			}
			for _, name := range []string{"composer", "chat", "sidebar", "modes", "stats"} {
				if sample.DOM[name] <= 0 || sample.Frame[name] < sample.DOM[name] {
					t.Errorf("missing/invalid startup observation %s: DOM=%v frame=%v", name, sample.DOM, sample.Frame)
				}
			}
			if len(sample.Actions) != 6 {
				t.Fatalf("actions=%d, want 6", len(sample.Actions))
			}
			for _, action := range sample.Actions {
				if action.Settled < action.Start || action.DOM["dependent"] < action.Start || action.Frame["dependent"] < action.DOM["dependent"] {
					t.Errorf("incomplete observations: %+v", action)
				}
				fields := []string{}
				if strings.HasPrefix(action.Name, "darwin") {
					fields = []string{"darwinModel", "darwinPool", "darwinEffort"}
				} else if strings.HasPrefix(action.Name, "review") {
					fields = []string{"reviewEffort"}
				}
				for _, field := range fields {
					if action.DOM[field] < action.Start || action.Frame[field] < action.DOM[field] {
						t.Errorf("%s missing individual field observation %s: %+v", action.Name, field, action)
					}
				}
				posts := 0
				for _, request := range sample.Requests {
					if request.Phase == action.Name && request.Path == "/api/config" && request.Method == "POST" {
						posts++
					}
				}
				if posts != 1 {
					t.Errorf("%s: config POST count=%d, want one semantic save", action.Name, posts)
				}
			}
			for _, request := range sample.Requests {
				if request.Status != http.StatusOK || request.End < request.Start {
					t.Errorf("failed/incomplete API request: %+v", request)
				}
			}
			mock.mu.Lock()
			if mock.values["darwin"] != "default" || mock.values["review_model"] != "" || mock.values["orchestrator"] != "off" ||
				mock.values["darwin_models"] != "fixture-main" || mock.values["darwin_pool_size"] != "3" || mock.values["darwin_reasoning_effort"] != "high" ||
				mock.values["review_reasoning_effort"] != "high" || mock.values["task_reasoning_effort"] != "medium" {
				t.Errorf("unexpected persisted config: %v", mock.values)
			}
			serverRequests := append([]sidebarServerRequest(nil), mock.requests...)
			mock.mu.Unlock()
			// Check every full save snapshot, including all untouched dependent
			// values. This verifies state/causality, not the old latency or GET count.
			expected := map[string]string{
				"orchestrator": "default", "task_model": "", "task_reasoning_effort": "medium",
				"advisor_model": "off", "advisor_reasoning_effort": "low", "review_model": "", "review_reasoning_effort": "high",
				"darwin": "default", "darwin_models": "fixture-main", "darwin_pool_size": "3", "darwin_reasoning_effort": "high",
			}
			posts := 0
			for _, request := range serverRequests {
				if request.Path == "/api/config" && request.Method == http.MethodPost {
					expected[request.Key] = request.Value
					if !maps.Equal(expected, request.SnapshotValues) {
						t.Errorf("save %s=%s lost config: got %v want %v", request.Key, request.Value, request.SnapshotValues, expected)
					}
					posts++
				}
			}
			if posts != 6 {
				t.Errorf("fixture config saves=%d, want 6", posts)
			}
			encoded, _ := json.Marshal(sample)
			t.Logf("SYNTHETIC sample=%d browser chronology (ms since navigation; end=body consumed; frame=visibility at rAF, not compositor presentation): %s", repeat+1, encoded)
			encoded, _ = json.Marshal(serverRequests)
			t.Logf("SYNTHETIC sample=%d server chronology (independent server-relative clock; ms includes controlled delay): %s", repeat+1, encoded)
			sidebarLogPaths(t, sample)
			samples = append(samples, sample)
		})
	}
	if len(samples) != 3 {
		t.Fatal("three complete independent samples are required for medians")
	}
	t.Log("SYNTHETIC medians of 3 fresh native browser profiles; health=40ms config GET=40ms stats=200ms config POST=80ms; full production page, no real server/model/config/auth/user data")
	for _, name := range []string{"composer", "chat", "sidebar", "modes", "stats"} {
		var dom, frame []float64
		for _, sample := range samples {
			dom = append(dom, sample.DOM[name])
			frame = append(frame, sample.Frame[name])
		}
		t.Logf("startup %-10s DOM=%6.1fms frame=%6.1fms", name, sidebarMedian(dom), sidebarMedian(frame))
	}
	var fcp []float64
	for _, sample := range samples {
		fcp = append(fcp, sample.Paints["first-contentful-paint"])
	}
	t.Logf("startup first-contentful-paint=%6.1fms", sidebarMedian(fcp))
	for i, action := range samples[0].Actions {
		var dom, frame, selected, settled []float64
		for _, sample := range samples {
			a := sample.Actions[i]
			dom = append(dom, a.DOM["dependent"]-a.Start)
			frame = append(frame, a.Frame["dependent"]-a.Start)
			selected = append(selected, a.Frame["selected"]-a.Start)
			settled = append(settled, a.Settled-a.Start)
		}
		t.Logf("click %-16s dependent DOM=%6.1fms frame=%6.1fms selected frame=%6.1fms settled=%6.1fms", action.Name, sidebarMedian(dom), sidebarMedian(frame), sidebarMedian(selected), sidebarMedian(settled))
	}
}

// testbrowser intentionally opens background targets and has no activation
// API. Use Chromium's native /json/activate endpoint on only the debug port
// published by the browser just launched under our private temporary root.
func sidebarActivateOwnedTarget(ctx context.Context, temp, address string) error {
	files, err := filepath.Glob(filepath.Join(temp, "supercli-testbrowser-*", "DevToolsActivePort"))
	if err != nil || len(files) != 1 {
		return fmt.Errorf("owned DevToolsActivePort: files=%v err=%v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(strings.SplitN(string(data), "\n", 2)[0])
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid owned debug port: %q", data)
	}
	origin := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	get := func(path string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
		if err != nil {
			return err
		}
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("owned CDP %s: %s", path, res.Status)
		}
		if out != nil {
			return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
		}
		_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
		return err
	}
	var targets []struct{ ID, URL string }
	if err := get("/json/list", &targets); err != nil {
		return err
	}
	for _, target := range targets {
		if target.URL == address {
			return get("/json/activate/"+target.ID, nil)
		}
	}
	return fmt.Errorf("owned preparation target not found: %s", address)
}

type sidebarRequest struct {
	Phase, Method, Path string
	Start, Headers, End float64
	Status              int
}

type sidebarAction struct {
	Name           string
	Start, Settled float64
	DOM, Frame     map[string]float64
}

type sidebarProfile struct {
	Width      int
	Visibility string
	DOM, Frame map[string]float64
	Paints     map[string]float64
	Requests   []sidebarRequest
	Actions    []sidebarAction
	Errors     []string
	Changes    []map[string]any
}

type sidebarServerRequest struct {
	Method, Path   string
	Start, End     float64
	ControlledMS   int64
	Key, Value     string            `json:",omitempty"`
	SnapshotValues map[string]string `json:",omitempty"`
}

type sidebarLatencyFixture struct {
	base     *fixture
	mu       sync.Mutex
	epoch    time.Time
	values   map[string]string
	requests []sidebarServerRequest
}

func (f *sidebarLatencyFixture) snapshotLocked() map[string]any {
	var knobs []map[string]any
	// Complete wire metadata for every knob consumed by the sidebar, rather
	// than only the switch: the POST returns this same full snapshot as GET.
	for _, key := range []string{"orchestrator", "task_model", "task_reasoning_effort", "advisor_model", "advisor_reasoning_effort", "review_model", "review_reasoning_effort", "darwin", "darwin_models", "darwin_pool_size", "darwin_reasoning_effort"} {
		raw, kind, state, def := f.values[key], "text", "", ""
		if key == "darwin" || key == "orchestrator" {
			kind, state, def = "tri", raw, "off"
			if key == "orchestrator" {
				def = "auto"
			}
			raw = ""
		} else if key == "darwin_pool_size" {
			kind, def = "int", "0"
		}
		source := "manual"
		if raw == "" && (state == "" || state == "default") {
			source = "default"
		}
		value := raw
		if state != "" {
			value = state
		}
		knobs = append(knobs, map[string]any{"key": key, "label": key, "desc": "Synthetic sidebar fixture", "kind": kind, "value": value, "source": source, "raw": raw, "state": state, "default": def, "next_session": true})
	}
	return map[string]any{"knobs": knobs, "cost_currencies": []string{"USD", "EUR"}}
}

func (f *sidebarLatencyFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// Even accidental frontend remote connections cannot leave the fixture.
	w.Header().Set("Content-Security-Policy", "connect-src 'self'; img-src 'self' data:; font-src 'self' data:")
	if r.URL.Path == "/__sidebar_prepare" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><title>Owned headless preparation target</title>")
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		f.base.serveHTTP(w, r)
		return
	}
	record := sidebarServerRequest{Method: r.Method, Path: r.URL.Path, Start: float64(time.Since(f.epoch).Microseconds()) / 1000}
	var delay time.Duration
	switch r.URL.Path {
	case "/api/health", "/api/config":
		delay = 40 * time.Millisecond
		if r.Method == http.MethodPost {
			delay = 80 * time.Millisecond
		}
	case "/api/stats":
		delay = 200 * time.Millisecond
	}
	record.ControlledMS = delay.Milliseconds()
	if r.URL.Path == "/api/config" && r.Method == http.MethodPost {
		var body struct{ Key, Value string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			http.Error(w, "bad fixture config", http.StatusBadRequest)
			return
		}
		record.Key, record.Value = body.Key, body.Value
	}
	if delay != 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
	}
	var result any
	switch r.URL.Path {
	case "/api/settings":
		result = map[string]any{"settings": map[string]any{"ui.sidebarHidden": false, "ui.lang": "en", "ui.resumeLastSession": false, "ui.rememberSessionRuntime": false}}
	case "/api/health":
		result = map[string]any{"ok": true, "chat_ready": true, "model": "fixture-main", "provider": "fixture", "home": "/fixture-only"}
	case "/api/stats":
		result = map[string]any{"model": "fixture-main", "session": map[string]any{"model": "fixture-session-stat", "provider": "fixture"}, "tokens": map[string]any{"input": 1234, "output": 56, "total": 1290}}
	case "/api/config":
		f.mu.Lock()
		if r.Method == http.MethodPost {
			if _, ok := f.values[record.Key]; !ok {
				f.mu.Unlock()
				http.Error(w, "unknown fixture knob", http.StatusBadRequest)
				return
			}
			f.values[record.Key] = record.Value
		}
		result = f.snapshotLocked()
		record.SnapshotValues = make(map[string]string, len(f.values))
		for key, value := range f.values {
			record.SnapshotValues[key] = value
		}
		f.mu.Unlock()
	case "/api/models":
		result = map[string]any{"active": "fixture-main", "provider": "fixture", "models": []map[string]any{
			{"id": "fixture-main", "provider": "fixture", "context_length": 32768},
			{"id": "fixture-review", "provider": "fixture", "context_length": 32768},
		}, "reasoning": sidebarFixtureReasoning()}
	case "/api/reasoning":
		result = sidebarFixtureReasoning()
	case "/api/sessions":
		result = []any{}
	case "/api/chat", "/api/model", "/api/sync/stream":
		http.Error(w, "not permitted in sidebar-only fixture", http.StatusForbidden)
	default:
		f.base.serveHTTP(w, r)
	}
	if result != nil {
		jsonResponse(w, result)
	}
	record.End = float64(time.Since(f.epoch).Microseconds()) / 1000
	f.mu.Lock()
	f.requests = append(f.requests, record)
	f.mu.Unlock()
}

func sidebarFixtureReasoning() map[string]any {
	return map[string]any{"configured": "medium", "effective": "medium", "selected": "medium", "adjusted": false, "supported": true, "toggle_only": false, "levels": []string{"none", "low", "medium", "high"}}
}

func sidebarMedian(values []float64) float64 {
	sort.Float64s(values)
	return values[len(values)/2]
}

func sidebarLogPaths(t *testing.T, sample sidebarProfile) {
	t.Helper()
	for _, phase := range []string{"startup", "darwin-on", "darwin-off", "review-on", "review-off", "orchestrator-on", "orchestrator-off"} {
		counts := map[string]int{}
		var paths []string
		origin := 0.0
		for _, action := range sample.Actions {
			if action.Name == phase {
				origin = action.Start
			}
		}
		for _, request := range sample.Requests {
			if request.Phase == phase {
				counts[request.Method+" "+request.Path]++
				paths = append(paths, fmt.Sprintf("%s %s @%.1f..%.1f (%.1fms)", request.Method, request.Path, request.Start-origin, request.End-origin, request.End-request.Start))
			}
		}
		t.Logf("critical path %-16s counts=%v requests=[%s]", phase, counts, strings.Join(paths, "; "))
	}
}

// This runs inline at the start of <head>, before CSS and all application
// scripts. It observes rather than replacing renderStats/init/toggle logic.
// Promise/MutationObserver timestamps and rAF-visible timestamps are distinct.
const sidebarLatencyProbe = `
(() => {
  'use strict';
  const p = window.__sidebarProbe = {width:innerWidth, dom:{}, frame:{}, paints:{}, requests:[], actions:[], errors:[], changes:[]};
  const now = () => performance.now();
  let phase = 'startup', action = null, stopped = false;
  const labels = {darwin:'stats.darwinTool', darwinModel:'stats.darwinModel', darwinPool:'stats.darwinPool', darwinEffort:'effort.darwin', review:'stats.reviewModel', reviewEffort:'effort.review', orchestrator:'stats.orchDesc', taskModel:'stats.taskModel', taskEffort:'stats.taskEffort'};
  const row = key => document.querySelector('#stats-block [data-i18n-text="'+labels[key]+'"]')?.closest('.stat-row');
  function visible(node) {
    if (!node || !node.isConnected) return false;
    const r = node.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0 || r.bottom <= 0 || r.right <= 0 || r.top >= innerHeight || r.left >= innerWidth) return false;
    for (let n = node; n; n = n.parentElement) {
      const css = getComputedStyle(n);
      if (n.hidden || css.display === 'none' || css.visibility === 'hidden' || css.opacity === '0') return false;
      if (n !== node && /(auto|scroll|hidden|clip)/.test(css.overflowY)) {
        const clip = n.getBoundingClientRect();
        if (r.top >= clip.bottom || r.bottom <= clip.top) return false;
      }
    }
    return true;
  }
  const shown = key => visible(row(key));
  const previous = {};
  function mark(target, key, condition, at) { if (condition && target[key] === undefined) target[key] = at; }
  function desired(a) {
    if (a.name.startsWith('darwin')) return ['darwinModel','darwinPool','darwinEffort'].every(k => shown(k) === a.on);
    if (a.name.startsWith('review')) return shown('reviewEffort') === a.on;
    // Production currently always shows task model/effort, even when off.
    // Orchestrator has no conditional dependent rows: measure their retention
    // and the selected state, then separately measure settled save/refresh.
    return shown('taskModel') && shown('taskEffort');
  }
  function selected(a) {
    if (a.name.startsWith('review')) {
      const text = row('review')?.querySelector('.orch-btn')?.textContent;
      return a.on ? text === 'fixture-review' : !!text && text !== 'fixture-review';
    }
    const key = a.name.startsWith('darwin') ? 'darwin' : 'orchestrator';
    const index = key === 'darwin' ? (a.on ? 0 : 1) : (a.on ? 1 : 2);
    return row(key)?.querySelectorAll('.seg button')[index]?.getAttribute('aria-pressed') === 'true';
  }
  function sample(kind) {
    const at = now(), target = kind === 'dom' ? p.dom : p.frame;
    mark(target, 'chat', visible(document.querySelector('#welcome')), at);
    mark(target, 'composer', visible(document.querySelector('#prompt')), at);
    mark(target, 'sidebar', visible(document.querySelector('#sidebar')), at);
    mark(target, 'modes', shown('darwin') && shown('review') && shown('orchestrator'), at);
    mark(target, 'stats', Array.from(document.querySelectorAll('#stats-block .stat-row .v')).some(n => n.textContent === 'fixture-session-stat' && visible(n)), at);
    if (kind === 'dom') for (const key of Object.keys(labels)) {
      const state = shown(key);
      if (previous[key] !== state) {
        previous[key] = state;
        p.changes.push({at, key, visible:state, phase});
      }
    }
    if (action) {
      const dest = kind === 'dom' ? action.dom : action.frame;
      mark(dest, 'selected', selected(action), at);
      mark(dest, 'dependent', desired(action), at);
      for (const key of action.fields) mark(dest, key, shown(key) === action.on, at);
    }
  }
  const waiters = new Set();
  function checkWaiters() { for (const check of Array.from(waiters)) check(); }
  function wait(predicate, description) {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => { waiters.delete(check); reject(new Error('sidebar diagnostic timeout: '+description)); }, 10000);
      function check() {
        if (predicate()) { clearTimeout(timer); waiters.delete(check); resolve(); }
      }
      waiters.add(check); check();
    });
  }
  const observer = new MutationObserver(() => { sample('dom'); checkWaiters(); });
  observer.observe(document, {subtree:true, childList:true, attributes:true, characterData:true});
  function frame() {
    if (stopped) return;
    // A frame can discover CSS-only visibility not accompanied by a mutation.
    sample('dom'); sample('frame'); checkWaiters(); requestAnimationFrame(frame);
  }
  requestAnimationFrame(frame);
  new PerformanceObserver(list => {
    for (const entry of list.getEntries()) p.paints[entry.name] = entry.startTime;
  }).observe({type:'paint', buffered:true});
  addEventListener('error', e => p.errors.push(e.error?.stack || e.message));
  addEventListener('unhandledrejection', e => p.errors.push(e.reason?.stack || String(e.reason)));
  const originalFetch = window.fetch.bind(window);
  window.fetch = async function(input, options) {
    const path = new URL(typeof input === 'string' ? input : input.url, location.href).pathname;
    if (!path.startsWith('/api/')) return originalFetch(input, options);
    const request = {phase, method:options?.method || input?.method || 'GET', path, start:now(), headers:0, end:0, status:0};
    p.requests.push(request);
    try {
      const response = await originalFetch(input, options);
      request.headers = now(); request.status = response.status;
      for (const method of ['json','text']) {
        const read = response[method].bind(response);
        response[method] = async () => {
          try { return await read(); }
          finally { request.end = now(); checkWaiters(); }
        };
      }
      return response;
    } catch (error) { request.end = now(); p.errors.push(String(error)); throw error; }
  };
  function idle() { return p.requests.every(r => r.end > 0); }
  function statsSettled() { return document.querySelector('#stats-block')?.getAttribute('aria-busy') === 'false'; }
  p.waitStartup = () => wait(() => p.frame.modes && p.frame.stats && idle() && statsSettled() && window.modelCache?.some(m => m.id === 'fixture-review'), 'startup visibility and API idle');
  p.run = async function(name) {
    phase = name;
    const on = name.endsWith('-on');
    action = {name, on, start:0, settled:0, dom:{}, frame:{}, fields:name.startsWith('darwin') ? ['darwinModel','darwinPool','darwinEffort'] : name.startsWith('review') ? ['reviewEffort'] : []};
    p.actions.push(action);
    let button;
    if (name.startsWith('review')) {
      const r = row('review');
      r.querySelector('.orch-btn').click();
      button = on ? Array.from(r.querySelectorAll('.prow')).find(n => n.querySelector('.pid')?.textContent === 'fixture-review') : r.querySelector('.prow');
    } else {
      const key = name.startsWith('darwin') ? 'darwin' : 'orchestrator';
      const index = key === 'darwin' ? (on ? 0 : 1) : (on ? 1 : 2);
      button = row(key)?.querySelectorAll('.seg button')[index];
    }
    if (!button || button.disabled || !visible(button)) throw new Error('unavailable semantic click target: '+name);
    action.start = now(); button.click(); sample('dom');
    await wait(() => {
      const saved = p.requests.some(r => r.phase === name && r.path === '/api/config' && r.method === 'POST' && r.status === 200 && r.end);
      const busy = Array.from(document.querySelectorAll('#stats-block .seg')).some(n => n.getAttribute('aria-busy') === 'true');
      return saved && idle() && statsSettled() && !busy && selected(action) && desired(action) && action.frame.dependent && action.frame.selected;
    }, name+' saved, current fields, selected state, frame and idle');
    action.settled = now();
    action = null;
  };
  p.finish = function() {
    stopped = true; observer.disconnect();
    return {width:innerWidth, visibility:document.visibilityState, dom:p.dom, frame:p.frame, paints:p.paints, requests:p.requests, actions:p.actions, errors:p.errors, changes:p.changes};
  };
})();
`
