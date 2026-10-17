package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"supercli/internal/testbrowser"
)

// Delivery tests use only in-memory queues and HTTP recorders. Marking once as
// done prevents call from opening a listener or contacting a real extension.
func newThunderbirdDeliveryTestState() *thunderbirdBridgeState {
	b := &thunderbirdBridgeState{
		queue:    make(chan thunderbirdBridgeRequest, 32),
		waiters:  make(map[string]chan thunderbirdBridgeResponse),
		lastPoll: time.Now(),
	}
	b.once.Do(func() {})
	return b
}

func postThunderbirdDeliveryResult(b *thunderbirdBridgeState, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/result?token="+thunderbirdBridgeToken, strings.NewReader(body))
	rec := httptest.NewRecorder()
	b.handleResult(rec, req)
	return rec
}

func TestThunderbirdDeliveryResultReplayAfterLostACK(t *testing.T) {
	for _, ok := range []bool{true, false} {
		t.Run(fmt.Sprintf("ok=%t", ok), func(t *testing.T) {
			b := newThunderbirdDeliveryTestState()
			waiter := make(chan thunderbirdBridgeResponse, 1)
			b.waiters["lost-ack"] = waiter
			body := fmt.Sprintf(`{"id":"lost-ack","ok":%t,"data":{"synthetic":true},"error":"synthetic failure"}`, ok)

			// Thunderbird cannot distinguish an accepted response with a lost
			// HTTP acknowledgement from a result that did not arrive at all.
			first := postThunderbirdDeliveryResult(b, body)
			if first.Code != http.StatusNoContent {
				t.Fatalf("first result: status=%d body=%s", first.Code, first.Body.String())
			}
			for i := 0; i < 3; i++ {
				replay := postThunderbirdDeliveryResult(b, body)
				if replay.Code != http.StatusNoContent {
					t.Fatalf("replay %d before consumption: status=%d body=%s", i, replay.Code, replay.Body.String())
				}
			}
			if len(waiter) != 1 {
				t.Fatalf("delivered %d responses, want exactly one", len(waiter))
			}
			got := <-waiter
			if got.ID != "lost-ack" || got.OK != ok || string(got.Data) != `{"synthetic":true}` || got.Error != "synthetic failure" {
				t.Fatalf("delivered result differs from original: %+v", got)
			}

			// Replaying after the channel was drained must not send it again,
			// even before call has finished removing its waiter.
			if rec := postThunderbirdDeliveryResult(b, body); rec.Code != http.StatusNoContent {
				t.Fatalf("replay after consumption: status=%d body=%s", rec.Code, rec.Body.String())
			}
			if len(waiter) != 0 {
				t.Fatal("an identical replay redelivered the result")
			}
			b.mu.Lock()
			delete(b.waiters, "lost-ack")
			b.mu.Unlock()
			if rec := postThunderbirdDeliveryResult(b, body); rec.Code != http.StatusNoContent {
				t.Fatalf("replay after caller cleanup: status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestThunderbirdDeliveryConcurrentReplaysDeliverOnce(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	waiter := make(chan thunderbirdBridgeResponse, 32)
	b.waiters["concurrent"] = waiter
	const body = `{"id":"concurrent","ok":true,"data":{"synthetic":true}}`
	const workers = 32
	codes := make(chan int, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes <- postThunderbirdDeliveryResult(b, body).Code
		}()
	}
	close(start)
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusNoContent {
			t.Errorf("concurrent result status=%d, want 204", code)
		}
	}
	if len(waiter) != 1 {
		t.Fatalf("concurrent replays delivered %d results, want exactly one", len(waiter))
	}
}

func TestThunderbirdDeliveryUnknownAndConflictingResults(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	const original = `{"id":"accepted","ok":true,"data":{"count":1}}`
	if rec := postThunderbirdDeliveryResult(b, original); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown result status=%d, want 404", rec.Code)
	}
	waiter := make(chan thunderbirdBridgeResponse, 1)
	b.waiters["accepted"] = waiter
	if rec := postThunderbirdDeliveryResult(b, original); rec.Code != http.StatusNoContent {
		t.Fatalf("first accepted result status=%d, want 204", rec.Code)
	}
	<-waiter
	for _, body := range []string{
		`{"id":"accepted","ok":true,"data":{"count":2}}`,
		`{"id":"accepted","ok":false,"data":{"count":1}}`,
		`{"id":"accepted","ok":true,"data":{"count":1},"error":"different"}`,
	} {
		if rec := postThunderbirdDeliveryResult(b, body); rec.Code != http.StatusConflict {
			t.Errorf("conflicting result status=%d, want 409; body=%s", rec.Code, body)
		}
	}
	if len(waiter) != 0 {
		t.Fatal("conflicting result was redelivered")
	}
	if rec := postThunderbirdDeliveryResult(b, original); rec.Code != http.StatusNoContent {
		t.Fatalf("conflict replaced original acceptance: status=%d", rec.Code)
	}
}

func TestThunderbirdDeliveryCompletionExpires(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	const body = `{"id":"expired","ok":true,"data":{"synthetic":true}}`
	b.waiters["expired"] = make(chan thunderbirdBridgeResponse, 1)
	if rec := postThunderbirdDeliveryResult(b, body); rec.Code != http.StatusNoContent {
		t.Fatalf("first result status=%d, want 204", rec.Code)
	}
	b.mu.Lock()
	delete(b.waiters, "expired")
	completion := b.completed["expired"]
	completion.at = time.Now().Add(-thunderbirdCompletionTTL)
	b.completed["expired"] = completion
	b.mu.Unlock()
	if rec := postThunderbirdDeliveryResult(b, body); rec.Code != http.StatusNotFound {
		t.Fatalf("expired replay status=%d, want 404", rec.Code)
	}
	if len(b.completed) != 0 {
		t.Fatalf("expired completion retained: %+v", b.completed)
	}
}

func TestThunderbirdDeliveryCompletionTTLBoundary(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	now := time.Now()
	b.completed = map[string]thunderbirdCompletion{
		"older":    {at: now.Add(-thunderbirdCompletionTTL - time.Nanosecond)},
		"boundary": {at: now.Add(-thunderbirdCompletionTTL)},
		"current":  {at: now.Add(-thunderbirdCompletionTTL + time.Nanosecond)},
	}
	b.mu.Lock()
	b.pruneCompletions(now)
	b.mu.Unlock()
	if len(b.completed) != 1 {
		t.Fatalf("TTL pruning kept %d completions, want 1", len(b.completed))
	}
	if _, ok := b.completed["current"]; !ok {
		t.Fatal("completion within the TTL was pruned")
	}
}

func TestThunderbirdDeliveryCompletionCacheBounded(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	if thunderbirdCompletionLimit != 256 || thunderbirdCompletionTTL != 5*time.Minute {
		t.Fatalf("unexpected acceptance window: limit=%d TTL=%s", thunderbirdCompletionLimit, thunderbirdCompletionTTL)
	}
	for i := 0; i <= thunderbirdCompletionLimit; i++ {
		id := fmt.Sprintf("bounded-%03d", i)
		waiter := make(chan thunderbirdBridgeResponse, 1)
		b.waiters[id] = waiter
		body := fmt.Sprintf(`{"id":%q,"ok":true,"data":{"synthetic":true}}`, id)
		if rec := postThunderbirdDeliveryResult(b, body); rec.Code != http.StatusNoContent {
			t.Fatalf("result %s status=%d, want 204", id, rec.Code)
		}
		<-waiter
		delete(b.waiters, id)
		if len(b.completed) > thunderbirdCompletionLimit {
			t.Fatalf("completion cache has %d entries, limit=%d", len(b.completed), thunderbirdCompletionLimit)
		}
	}
	if len(b.completed) != thunderbirdCompletionLimit {
		t.Fatalf("cache has %d entries, want %d", len(b.completed), thunderbirdCompletionLimit)
	}
	first := `{"id":"bounded-000","ok":true,"data":{"synthetic":true}}`
	if rec := postThunderbirdDeliveryResult(b, first); rec.Code != http.StatusNotFound {
		t.Fatalf("oldest evicted result status=%d, want 404", rec.Code)
	}
	last := fmt.Sprintf(`{"id":"bounded-%03d","ok":true,"data":{"synthetic":true}}`, thunderbirdCompletionLimit)
	if rec := postThunderbirdDeliveryResult(b, last); rec.Code != http.StatusNoContent {
		t.Fatalf("newest result replay status=%d, want 204", rec.Code)
	}
}

func TestThunderbirdDeliveryPollSkipsAbandonedRequests(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	for i := 0; i < 3; i++ {
		b.queue <- thunderbirdBridgeRequest{ID: fmt.Sprintf("abandoned-%d", i), Op: "move", Args: json.RawMessage(`{"synthetic":true}`)}
	}
	want := thunderbirdBridgeRequest{ID: "live-request", Op: "count", Args: json.RawMessage(`{"synthetic":true}`)}
	b.waiters[want.ID] = make(chan thunderbirdBridgeResponse, 1)
	b.queue <- want
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/poll?token="+thunderbirdBridgeToken, nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	b.handlePoll(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("poll status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got thunderbirdBridgeRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode poll: %v; body=%s", err, rec.Body.String())
	}
	if got.ID != want.ID || got.Op != want.Op || !bytes.Equal(got.Args, want.Args) {
		t.Fatalf("poll returned %+v, want live request %+v", got, want)
	}
	if len(b.queue) != 0 {
		t.Fatalf("poll left %d requests queued", len(b.queue))
	}
	if b.activePolls != 0 {
		t.Fatalf("poll leaked active poll count: %d", b.activePolls)
	}
}

func TestThunderbirdDeliveryPreCancelledCallNeverQueues(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Repetition guards against a select randomly enqueueing when both a
	// cancelled context and the queue's writable case are ready.
	for i := 0; i < 100; i++ {
		_, err := b.call(ctx, "move", json.RawMessage(`{"synthetic":true}`))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-cancelled call error=%v, want context.Canceled", err)
		}
		if len(b.queue) != 0 {
			t.Fatalf("pre-cancelled call %d queued a request", i)
		}
		if len(b.waiters) != 0 {
			t.Fatalf("pre-cancelled call %d leaked %d waiters", i, len(b.waiters))
		}
	}
}

func TestThunderbirdDeliveryDispatchedCancellationReportsUnknownAndUsesFreshIDs(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	var previousID string
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		errs := make(chan error, 1)
		go func() {
			_, err := b.call(ctx, "move", json.RawMessage(`{"synthetic":true}`))
			errs <- err
		}()
		pollCtx, stopPoll := context.WithTimeout(context.Background(), time.Second)
		poll := httptest.NewRequest(http.MethodGet, "/poll?token="+thunderbirdBridgeToken, nil).WithContext(pollCtx)
		rec := httptest.NewRecorder()
		b.handlePoll(rec, poll)
		stopPoll()
		var req thunderbirdBridgeRequest
		if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
			cancel()
			t.Fatalf("poll did not dispatch the synthetic request: %v; body=%s", err, rec.Body.String())
		}
		cancel()
		select {
		case err := <-errs:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("queued call error=%v, want wrapped context.Canceled", err)
			}
			message := strings.ToLower(err.Error())
			for _, fragment := range []string{"outcome", "unknown", "do not", "retry", req.ID} {
				if !strings.Contains(message, fragment) {
					t.Errorf("queued call error %q lacks %q", err, fragment)
				}
			}
		case <-time.After(time.Second):
			t.Fatal("queued call did not stop after cancellation")
		}
		if req.ID == "" || req.ID == previousID {
			t.Fatalf("new call reused request ID %q", req.ID)
		}
		previousID = req.ID
		if len(b.queue) != 0 {
			t.Fatal("cancelled call automatically queued another request")
		}
		b.mu.Lock()
		remaining := len(b.waiters)
		b.mu.Unlock()
		if remaining != 0 {
			t.Fatalf("cancelled call left %d waiters", remaining)
		}
		late := fmt.Sprintf(`{"id":%q,"ok":true,"data":{"synthetic":true}}`, req.ID)
		if rec := postThunderbirdDeliveryResult(b, late); rec.Code != http.StatusNotFound {
			t.Fatalf("unaccepted late result status=%d, want 404", rec.Code)
		}
	}
}

func TestThunderbirdDeliveryCancellationBeforeDispatchPreventsExecution(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 1)
	go func() {
		_, err := b.call(ctx, "move", json.RawMessage(`{"synthetic":true}`))
		errs <- err
	}()
	var abandoned thunderbirdBridgeRequest
	select {
	case abandoned = <-b.queue:
		// Observe enqueueing without dispatching through handlePoll, then
		// restore the request to exercise the abandoned-queue cleanup.
		b.queue <- abandoned
	case <-time.After(time.Second):
		t.Fatal("call did not queue its synthetic request")
	}
	cancel()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("undispatched cancellation error=%v, want context.Canceled", err)
		}
		for _, fragment := range []string{"cancelled before dispatch", "operation not executed", abandoned.ID} {
			if !strings.Contains(err.Error(), fragment) {
				t.Errorf("undispatched cancellation error %q lacks %q", err, fragment)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("undispatched call did not stop after cancellation")
	}
	live := thunderbirdBridgeRequest{ID: "next-live", Op: "count", Args: json.RawMessage(`{}`)}
	b.mu.Lock()
	if len(b.waiters) != 0 || len(b.dispatched) != 0 {
		b.mu.Unlock()
		t.Fatal("undispatched cancellation retained request state")
	}
	b.waiters[live.ID] = make(chan thunderbirdBridgeResponse, 1)
	b.mu.Unlock()
	b.queue <- live
	pollCtx, stopPoll := context.WithTimeout(context.Background(), time.Second)
	defer stopPoll()
	poll := httptest.NewRequest(http.MethodGet, "/poll?token="+thunderbirdBridgeToken, nil).WithContext(pollCtx)
	rec := httptest.NewRecorder()
	b.handlePoll(rec, poll)
	var got thunderbirdBridgeRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.ID != live.ID {
		t.Fatalf("poll returned abandoned request instead of live request: got=%+v err=%v body=%s", got, err, rec.Body.String())
	}
}

func TestThunderbirdDeliveryDispatchedDeadlinePreservesContextError(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	errs := make(chan error, 1)
	go func() {
		_, err := b.call(ctx, "move", json.RawMessage(`{"synthetic":true}`))
		errs <- err
	}()
	pollCtx, stopPoll := context.WithTimeout(context.Background(), time.Second)
	defer stopPoll()
	poll := httptest.NewRequest(http.MethodGet, "/poll?token="+thunderbirdBridgeToken, nil).WithContext(pollCtx)
	rec := httptest.NewRecorder()
	b.handlePoll(rec, poll)
	var req thunderbirdBridgeRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatalf("poll did not dispatch request: %v; body=%s", err, rec.Body.String())
	}
	select {
	case err := <-errs:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline error=%v, want wrapped context.DeadlineExceeded", err)
		}
		if !strings.Contains(err.Error(), "outcome unknown") || !strings.Contains(err.Error(), req.ID) {
			t.Fatalf("deadline lost uncertainty or request ID: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatched call did not stop at context deadline")
	}
}

// Opt in with THUNDERBIRD_EXTENSION_SOURCE=/absolute/path/to/background.js.
// This connects the actual host and extension transport through a temporary
// loopback server; every messenger API is a synthetic in-memory fixture.
func TestThunderbirdDeliveryExtensionLostACKE2E(t *testing.T) {
	source := os.Getenv("THUNDERBIRD_EXTENSION_SOURCE")
	if source == "" {
		t.Skip("set THUNDERBIRD_EXTENSION_SOURCE to run the synthetic host/extension integration test")
	}
	if !filepath.IsAbs(source) {
		t.Fatal("THUNDERBIRD_EXTENSION_SOURCE must be an absolute file path")
	}
	if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("extension source is not a readable regular file: %s (%v)", source, err)
	}
	code, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read explicitly selected extension source: %v", err)
	}
	runThunderbirdDeliveryBrowserFixture(t, string(code))
}

func runThunderbirdDeliveryBrowserFixture(t *testing.T, source string) {
	t.Helper()
	b := newThunderbirdDeliveryTestState()
	mux := http.NewServeMux()
	mux.HandleFunc("/poll", b.handlePoll)
	mux.HandleFunc("/result", b.handleResult)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><head><title>Synthetic delivery fixture</title></head><body></body></html>"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	bridgeURL := regexp.MustCompile(`(?m)^const BRIDGE_URL = "http://127\.0\.0\.1:47831";$`)
	if len(bridgeURL.FindAllStringIndex(source, -1)) != 1 {
		t.Fatal("expected exactly one constant loopback BRIDGE_URL in extension source")
	}
	target, _ := json.Marshal(server.URL)
	source = bridgeURL.ReplaceAllStringFunc(source, func(string) string { return "const BRIDGE_URL = " + string(target) + ";" })
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	browser, err := testbrowser.Start(ctx, testbrowser.Options{})
	if err != nil {
		t.Fatal(err) // an opted-in native browser test must not skip missing Chrome
	}
	defer func() {
		if err := browser.Close(); err != nil {
			t.Error(err)
		}
	}()
	page, err := browser.NewPage(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	type operationResult struct {
		data json.RawMessage
		err  error
	}
	called := make(chan operationResult, 1)
	go func() {
		data, err := b.call(ctx, "create_folder", json.RawMessage(`{"op":"create_folder","confirm":true,"name":"Synthetic delivery fixture"}`))
		called <- operationResult{data, err}
	}()
	// Source is read by Go, then executed unchanged apart from its fixed bridge
	// origin. The new page has no real extension/mail APIs: only these stubs.
	expression := "(async () => { const fixtureTarget = " + string(target) + ";\n" + thunderbirdDeliveryBrowserFixture +
		"\nconst fixtureWorker = (() => {\n" + source + "\nreturn () => requestWorkerRunning;\n})();\n" + thunderbirdDeliveryBrowserAssertions + "\n})()"
	var output json.RawMessage
	if err := page.Eval(ctx, expression, &output); err != nil {
		cancel()
		t.Fatalf("native synthetic extension fixture: %v", err)
	}
	var operation operationResult
	select {
	case operation = <-called:
	case <-ctx.Done():
		t.Fatalf("synthetic host call did not finish: %v", ctx.Err())
	}
	if operation.err != nil {
		t.Fatalf("synthetic host call failed: %v; browser=%s", operation.err, output)
	}
	data := operation.data
	var result struct {
		Changed bool `json:"changed"`
		Created struct {
			Name string `json:"name"`
		} `json:"created"`
	}
	if err := json.Unmarshal(data, &result); err != nil || !result.Changed || result.Created.Name != "Synthetic delivery fixture" {
		t.Fatalf("unexpected synthetic operation result: %s (decode=%v)", data, err)
	}
	var report struct {
		Mutations       int    `json:"mutations"`
		Posts           int    `json:"posts"`
		IdenticalBodies bool   `json:"identicalBodies"`
		Statuses        []int  `json:"statuses"`
		ID              string `json:"id"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("invalid native browser fixture report: %v\n%s", err, output)
	}
	if report.Mutations != 1 || report.Posts != 2 || !report.IdenticalBodies || len(report.Statuses) != 2 || report.Statuses[0] != 204 || report.Statuses[1] != 204 {
		t.Fatalf("lost-ACK recovery violated single execution: %+v", report)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.completed[report.ID]; !ok || len(b.completed) != 1 || len(b.waiters) != 0 || len(b.dispatched) != 0 || len(b.queue) != 0 || b.requestID.Load() != 1 {
		t.Fatalf("host did not finish exactly one synthetic request: accepted=%d waiters=%d dispatched=%d queued=%d requestIDs=%d", len(b.completed), len(b.waiters), len(b.dispatched), len(b.queue), b.requestID.Load())
	}
}

const thunderbirdDeliveryBrowserFixture = `
const fixtureCheck = (condition, message) => { if (!condition) throw Error(message); };
const fixtureFolders = [], fixtureBodies = [], fixtureStatuses = [];
let fixtureMutations = 0, fixturePolls = 0;
const fixtureAccount = {id:"synthetic-account", name:"Synthetic account", type:"none"};
const fixtureNativeFetch = globalThis.fetch.bind(globalThis);
const fixtureNativeTimeout = globalThis.setTimeout.bind(globalThis);
globalThis.messenger = {
 accounts: {list:async()=>[fixtureAccount]},
 folders: {
  query:async()=>fixtureFolders,
  create:async(_destination,name)=>{
   fixtureMutations++;
   const folder={id:"synthetic-folder",accountId:fixtureAccount.id,name,path:"/"+name};
   fixtureFolders.push(folder);return folder;
  }
 }
};
globalThis.browser = globalThis.messenger;
globalThis.console = {log(){},warn(){}};
globalThis.setTimeout = (fn,ms,...args)=>fixtureNativeTimeout(fn,ms===5000?ms:0,...args);
globalThis.fetch = async(url,options)=>{
 const parsed=new URL(url,location.href);
 fixtureCheck(parsed.origin===fixtureTarget,"fixture must never contact a real bridge");
 if(parsed.pathname==="/poll"){
  fixturePolls++;
  // One real host poll; leave any subsequent background loop dormant.
  if(fixturePolls>1)return new Promise(()=>{});
  return fixtureNativeFetch(url,options);
 }
 fixtureCheck(parsed.pathname==="/result","unexpected fixture endpoint");
 fixtureBodies.push(options.body);
 const response=await fixtureNativeFetch(url,options);fixtureStatuses.push(response.status);
 if(fixtureBodies.length===1){
  // Host acceptance is real; lose only the extension's knowledge of its ACK.
  await response.arrayBuffer();throw Error("synthetic lost HTTP acknowledgement");
 }
 return response;
};
`

const thunderbirdDeliveryBrowserAssertions = `
const fixtureDeadline=Date.now()+7000;
while(Date.now()<fixtureDeadline){
 if(fixtureBodies.length>=2 && !fixtureWorker())break;
 await new Promise(resolve=>fixtureNativeTimeout(resolve,2));
}
fixtureCheck(fixtureMutations===1,"lost ACK repeated the mailbox operation");
fixtureCheck(fixtureBodies.length===2,"lost ACK must retry delivery once");
fixtureCheck(fixtureBodies[0]===fixtureBodies[1],"retry must use identical serialized result");
fixtureCheck(JSON.stringify(fixtureStatuses)==="[204,204]","host did not acknowledge both posts");
fixtureCheck(JSON.parse(fixtureBodies[0]).ok===true,"operation failed");
fixtureCheck(fixtureWorker()===false,"extension worker did not stop");
return {mutations:fixtureMutations,posts:fixtureBodies.length,identicalBodies:true,statuses:fixtureStatuses,id:JSON.parse(fixtureBodies[0]).id};
`

// Always exercise the native harness with self-contained synthetic source.
// This does not opt in to or claim coverage of an external extension checkout.
func TestThunderbirdDeliveryNativeBrowserFixtureLostACK(t *testing.T) {
	token, _ := json.Marshal(thunderbirdBridgeToken)
	source := `const BRIDGE_URL = "http://127.0.0.1:47831";
let requestWorkerRunning=false;
const BRIDGE_TOKEN=` + string(token) + `;
(async()=>{
 const response=await fetch(BRIDGE_URL+"/poll?token="+encodeURIComponent(BRIDGE_TOKEN));
 const request=await response.json();
 if(request.op!=="create_folder" || !request.args.confirm)throw Error("unexpected synthetic operation");
 requestWorkerRunning=true;
 const account=(await messenger.accounts.list())[0];
 const created=await messenger.folders.create(account.id,request.args.name);
 const body=JSON.stringify({id:request.id,ok:true,data:{changed:true,created}});
 for(;;){
  try{
   const ack=await fetch(BRIDGE_URL+"/result?token="+encodeURIComponent(BRIDGE_TOKEN),{method:"POST",headers:{"Content-Type":"application/json"},body});
   if(!ack.ok)throw Error("result rejected");
   break;
  }catch(error){await new Promise(resolve=>setTimeout(resolve,10));}
 }
 requestWorkerRunning=false;
 await fetch(BRIDGE_URL+"/poll?token="+encodeURIComponent(BRIDGE_TOKEN));
})();`
	// Exercise Go's opted-in file-reading path too, but select only source
	// written by this test, never an installed extension or user's mailbox.
	path := filepath.Join(t.TempDir(), "background.js")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("THUNDERBIRD_EXTENSION_SOURCE", path)
	TestThunderbirdDeliveryExtensionLostACKE2E(t)
}

func TestThunderbirdDeliveryAuthenticationPrecedesRecoveryCache(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	b.waiters["auth-fixture"] = make(chan thunderbirdBridgeResponse, 1)
	const body = `{"id":"auth-fixture","ok":true,"data":{"synthetic":true}}`
	if got := postThunderbirdDeliveryResult(b, body); got.Code != http.StatusNoContent {
		t.Fatal(got.Code)
	}
	for _, token := range []string{"", "invalid-fixture-token"} {
		req := httptest.NewRequest(http.MethodPost, "/result?token="+token, strings.NewReader(body))
		rec := httptest.NewRecorder()
		b.handleResult(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("unauthorized replay status=%d", rec.Code)
		}
	}
	if len(b.completed) != 1 || len(b.waiters["auth-fixture"]) != 1 {
		t.Fatal("unauthorized replay changed recovery state")
	}
}

func TestThunderbirdDeliveryCompletionCacheKeepsAcceptanceOrderAtEqualTimes(t *testing.T) {
	b := newThunderbirdDeliveryTestState()
	sameTime := time.Now().Add(-time.Minute)
	firstID := fmt.Sprintf("tie-%03d", thunderbirdCompletionLimit)
	for i := 0; i < thunderbirdCompletionLimit; i++ {
		id := fmt.Sprintf("tie-%03d", thunderbirdCompletionLimit-i)
		waiter := make(chan thunderbirdBridgeResponse, 1)
		b.waiters[id] = waiter
		body := fmt.Sprintf(`{"id":%q,"ok":true,"data":{"synthetic":true}}`, id)
		if rec := postThunderbirdDeliveryResult(b, body); rec.Code != http.StatusNoContent {
			t.Fatal(rec.Code)
		}
		<-waiter
		b.mu.Lock()
		delete(b.waiters, id)
		completion := b.completed[id]
		completion.at = sameTime // Force a coarse Windows clock without depending on its resolution.
		b.completed[id] = completion
		b.mu.Unlock()
	}
	b.waiters["tie-newest"] = make(chan thunderbirdBridgeResponse, 1)
	newest := `{"id":"tie-newest","ok":true,"data":{"synthetic":true}}`
	if rec := postThunderbirdDeliveryResult(b, newest); rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	oldest := fmt.Sprintf(`{"id":%q,"ok":true,"data":{"synthetic":true}}`, firstID)
	if rec := postThunderbirdDeliveryResult(b, oldest); rec.Code != http.StatusNotFound {
		t.Fatalf("first accepted result survived tied-time eviction: status=%d", rec.Code)
	}
	if rec := postThunderbirdDeliveryResult(b, newest); rec.Code != http.StatusNoContent {
		t.Fatalf("newest result lost its recovery receipt: status=%d", rec.Code)
	}
	if len(b.completed) != thunderbirdCompletionLimit {
		t.Fatal("receipt cache exceeded its limit")
	}
}
