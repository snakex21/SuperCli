package webgui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/session"
)

// Live profiling: with ~400 saved workers the GUI's worker status polling
// read ~156 MB/s, because every /api/workers request selected and decoded
// every worker's full transcript (messages_json), twice. A listing reads
// only the light columns now.
func TestWorkerListingDoesNotReadTranscripts(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a large fixture")
	}
	srv := newTestServer(t, false)
	store, err := srv.eng.sessionStore()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create(srv.eng.Home(), "fixture", "many workers")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const workers = 400
	big := strings.Repeat("BUILD line: zombie spawn rule checked\n", (1<<20)/38)
	for i := 0; i < workers; i++ {
		status := "done"
		if i%100 == 0 {
			status = "running" // stale: no runner in this process
		}
		row := session.WorkerRow{SessionID: sess.ID, ID: fmt.Sprintf("worker-%d", i+1), Agent: "code", Description: "task", Model: "m",
			Status: status, Runs: 1, Messages: []llm.Message{{Role: llm.RoleUser, Content: "go"}, {Role: llm.RoleTool, Content: big}}, UpdatedAt: time.Now()}
		if err := store.SaveWorker(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	list := func() (time.Duration, int64, []webWorker) {
		readBefore := processReadBytes()
		start := time.Now()
		rec := httptest.NewRecorder()
		srv.handleWorkers(rec, httptest.NewRequest(http.MethodGet, "/api/workers?session="+sess.ID, nil))
		took := time.Since(start)
		read := processReadBytes() - readBefore
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		var body struct {
			Workers []webWorker `json:"workers"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return took, read, body.Workers
	}
	// The first call reconciles the stale "running" rows.
	_, _, first := list()
	interrupted := 0
	for _, w := range first {
		if w.Status == "interrupted" {
			interrupted++
		}
	}
	if len(first) != workers || interrupted != workers/100 {
		t.Fatalf("listing: %d workers, %d reconciled (want %d, %d)", len(first), interrupted, workers, workers/100)
	}
	took, read, _ := list()
	t.Logf("one /api/workers call with %d workers x 1 MB history: %s, %.1f MB read by the process", workers, took.Round(time.Millisecond), float64(read)/(1<<20))
	if read > 8<<20 {
		t.Fatalf("a listing read %.1f MB (transcripts?)", float64(read)/(1<<20))
	}
	if took > 50*time.Millisecond {
		t.Fatalf("a listing took %s", took)
	}
}
