package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/testbrowser"
)

func legacyObservationsFixture() (llm.Message, string, string) {
	observations := "[Worker worker-5 run 1 tool observations at 2026-10-18T20:18:43Z; historical snapshots, not current workspace state; outputs may be truncated]\n== read_lines {\"file\":\"żółć.go\"} ==\nORIGINAL_RESULT <script>window.observationInjected=true</script>"
	handle := "out_abcdef123456"
	retained := legacyWorkerNotice + "\n\n" + observations
	content := legacyWorkerNotice + "\n[Attached: worker tool observations (historical snapshots).]" + fmt.Sprintf("\n[stored tool output: %d bytes; handle=%s; read_output {\"handle\":%q}; saved]", len(retained), handle, handle)
	return llm.Message{Role: llm.RoleTool, Name: "task", ToolCallID: "fixture-delegate", Content: content}, handle, retained
}

func saveLegacyObservationFixture(t *testing.T, srv *Server, msg llm.Message, handle, retained string) (session.Session, session.TranscriptMessage) {
	t.Helper()
	ctx := t.Context()
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "saved worker observations")
	if err != nil {
		t.Fatal(err)
	}
	writer := session.NewWriter(store, sess.ID)
	if err = writer.SaveToolOutput(ctx, handle, retained); err != nil {
		t.Fatal(err)
	}
	if err = writer.AppendMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	row, err := store.ReadTranscriptMessageAt(ctx, sess.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	return sess, row
}

func TestWorkerLegacyObservationsReadScopeAndReceipt(t *testing.T) {
	srv := newTestServer(t, false)
	msg, handle, retained := legacyObservationsFixture()
	sess, row := saveLegacyObservationFixture(t, srv, msg, handle, retained)
	id := strconv.FormatInt(row.MessageID, 10)
	text, err := srv.eng.workerObservations(t.Context(), sess.ID, 1, id)
	if err != nil || !strings.Contains(text, "ORIGINAL_RESULT") {
		t.Fatalf("saved observations missing: %v", err)
	}
	for _, tc := range []struct {
		session string
		seq     int
		id      string
	}{{sess.ID, 1, "999999"}, {sess.ID, 0, id}, {"missing", 1, id}} {
		if _, err := srv.eng.workerObservations(t.Context(), tc.session, tc.seq, tc.id); err == nil {
			t.Fatal("invalid/stale receipt exposed observations")
		}
	}
	store, _ := srv.eng.sessionStore()
	other, err := store.Create(t.TempDir(), "fixture", "other workspace")
	if err != nil {
		t.Fatal(err)
	}
	if err = session.NewWriter(store, other.ID).AppendMessage(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.eng.workerObservations(t.Context(), other.ID, 1, id); err == nil {
		t.Fatal("foreign workspace exposed observations")
	}
	other, err = store.Create(srv.eng.Home(), "fixture", "other session")
	if err != nil {
		t.Fatal(err)
	}
	if err = session.NewWriter(store, other.ID).AppendMessage(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	otherRow, _ := store.ReadTranscriptMessageAt(t.Context(), other.ID, 1)
	if _, err = srv.eng.workerObservations(t.Context(), other.ID, 1, strconv.FormatInt(otherRow.MessageID, 10)); err == nil {
		t.Fatal("foreign session output handle was accepted")
	}
	for _, bad := range []llm.Message{
		{Role: llm.RoleUser, Content: msg.Content, Origin: &llm.ContextOrigin{Source: llm.SourceHuman}},
		{Role: llm.RoleTool, Name: "read_lines", ToolCallID: "read", Content: msg.Content},
		{Role: llm.RoleTool, Name: "task", ToolCallID: "delegate", Content: "quoted " + msg.Content},
	} {
		if _, ok := legacyWorkerObservations(bad); ok {
			t.Fatal("non-receipt became worker observations")
		}
	}
	inline := msg
	inline.Content = retained
	if _, ok := legacyWorkerObservations(inline); !ok {
		t.Fatal("inline legacy observations lost")
	}
	wrong := strings.Replace(retained, "[Worker worker-5 run 1", "[Worker worker-6 run 1", 1)
	wrongMsg, wrongHandle, _ := legacyObservationsFixture()
	wrongHandle = "out_abcdef654321"
	wrongMsg.Content = strings.ReplaceAll(wrongMsg.Content, handle, wrongHandle)
	wrongSession, wrongRow := saveLegacyObservationFixture(t, srv, wrongMsg, wrongHandle, wrong)
	if _, err = srv.eng.workerObservations(t.Context(), wrongSession.ID, 1, strconv.FormatInt(wrongRow.MessageID, 10)); err == nil {
		t.Fatal("foreign worker observations accepted")
	}
	before, _ := store.ReadMessages(t.Context(), sess.ID)
	if err = store.SaveContextProjection(t.Context(), sess.ID, []llm.Message{{Role: llm.RoleUser, Content: "short model summary"}}); err != nil {
		t.Fatal(err)
	}
	_, err = srv.eng.workerObservations(t.Context(), sess.ID, 1, id)
	after, _ := store.ReadMessages(t.Context(), sess.ID)
	if err != nil || len(before) != len(after) || before[0].Content != after[0].Content {
		t.Fatal("projection/read changed the human transcript")
	}
}

func TestWorkerLegacyObservationsNativeBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	srv := newTestServer(t, false)
	msg, handle, retained := legacyObservationsFixture()
	sess, _ := saveLegacyObservationFixture(t, srv, msg, handle, retained)
	messages, err := srv.eng.transcript(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].WorkerObservationsID == "" || len(messages[0].WorkerActivity) != 0 {
		t.Fatal("legacy receipt is not lazily inspectable")
	}
	data, _ := json.Marshal(messages)
	catalog, _ := assetsFS.ReadFile("assets/locales/pl.json")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><html><head><link rel="stylesheet" href="/app.css"></head><body><script>var fixtureCatalog=%s;</script><script src="/runtime.js"></script><script src="/00-helpers.js"></script><script src="/prelude.js"></script><script src="/04-transcript.js"></script><script src="/08-sessions.js"></script><script src="/ready.js"></script></body></html>`, catalog)
		case "/runtime.js":
			b, _ := sharedUIFS.ReadFile("shared-ui/runtime.js")
			w.Header().Set("Content-Type", "text/javascript")
			w.Write(b)
		case "/prelude.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, queueEditBrowserPrelude+`var preferences={};function registerLanguageRefresh(){}function toolDisclosureOpen(){return true;}function thinkingDisclosureOpen(){return false;}function renderText(s){var e=document.createElement('div');e.textContent=s;return e.innerHTML;}function fmtInteger(n){return String(n);}function fmtCompactNumber(n){return String(n);}function renderSentAttachments(){}function sentAttachmentsFor(){return [];}function userMessageDisplayText(s){return s;}`)
		case "/ready.js":
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprintf(w, `activeSessionID=transcriptSessionID=%q;smartScroll=function(){};var fixtureMessages=%s;`, sess.ID, data)
		case "/api/workers/observations":
			requests.Add(1)
			srv.handleWorkerObservations(w, r)
		case "/app.css":
			b, _ := assetsFS.ReadFile("assets/app.css")
			w.Header().Set("Content-Type", "text/css")
			w.Write(b)
		default:
			b, e := assetsFS.ReadFile("assets/js" + r.URL.Path)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/javascript")
			w.Write(b)
		}
	}))
	defer server.Close()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{Width: 1100, Height: 900})
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Close()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var failure string
	if err = page.Eval(ctx, `(async()=>{try{
function check(ok,why){if(!ok)throw Error(why);}
stream.appendChild(buildHistoryFragment(fixtureMessages,null,false));var row=stream.querySelector('.task-row'),detail=row.querySelector('.worker-observations');
check(detail&&!detail.open&&!row._workerObservationRequest,'history eagerly fetched observations');
check(getComputedStyle(detail.querySelector('summary'),'::before').content.includes('›'),'closed archive shows the expanded caret');
bindWorkerObservations(row,fixtureMessages[0]);bindWorkerObservations(row,fixtureMessages[0]);
check(row.querySelectorAll('.worker-observations').length===1,'repeated history binding duplicated observations');
check(!row.textContent.includes(t('task.activityUnavailable')),'available legacy observations labelled missing');
detail.open=true;detail.dispatchEvent(new Event('toggle'));await row._workerObservationRequest;
check(detail.textContent.includes('ORIGINAL_RESULT')&&detail.textContent.includes('żółć.go'),'saved tool details not displayed');
check(getComputedStyle(detail.querySelector('summary'),'::before').content.includes('⌄'),'open archive shows the collapsed caret');
check(!window.observationInjected&&!detail.querySelector('script'),'observations executed as HTML');
check(!row.querySelector('.task-activity-item.done'),'legacy evidence invented successful calls');
detail.open=false;detail.dispatchEvent(new Event('toggle'));detail.open=true;detail.dispatchEvent(new Event('toggle'));await row._workerObservationRequest;
check(detail.textContent.includes('ORIGINAL_RESULT'),'reopening lost the human log');
row.open=false;row.dispatchEvent(new Event('toggle'));row.open=true;row.dispatchEvent(new Event('toggle'));
check(row.querySelector('.worker-observations')===detail,'card expansion discarded saved observations');
return '';}catch(e){return e.stack||String(e);}})()`, &failure); err != nil {
		t.Fatal(err)
	}
	if failure != "" {
		t.Fatal(failure)
	}
	if requests.Load() != 1 {
		t.Fatalf("observations fetched %d times; want once on explicit expansion", requests.Load())
	}
	if stage := os.Getenv("SUPERCLI_WORKER_UI_STAGE"); stage != "" {
		shot, e := page.Screenshot(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(stage, "legacy-observations.png"), shot, 0600); e != nil {
			t.Fatal(e)
		}
	}
}

// An optional local copy of the user's exact legacy row/cache reproduces the
// screenshot without writing to the real database or shipping private text.
func TestWorkerLegacyObservationLocalSample(t *testing.T) {
	path := os.Getenv("SUPERCLI_WORKER_LEGACY_SAMPLE")
	if path == "" {
		t.Skip("optional local legacy sample")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var sample map[string]string
	if err = json.Unmarshal(data, &sample); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, false)
	msg := llm.Message{Role: llm.RoleTool, Name: sample["name"], ToolCallID: sample["call"], Content: sample["content"]}
	sess, row := saveLegacyObservationFixture(t, srv, msg, sample["handle"], sample["retained"])
	text, err := srv.eng.workerObservations(t.Context(), sess.ID, 1, strconv.FormatInt(row.MessageID, 10))
	if err != nil || len(text) < 1000 {
		t.Fatalf("actual legacy receipt not recovered: %v", err)
	}
	t.Logf("recovered %d bytes of saved historical observations without model calls", len(text))
}
