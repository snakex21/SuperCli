package webgui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionActivityPublishedAfterMessageIsSaved(t *testing.T) {
	srv := newTestServer(t, false)
	oldID := seedWebSession(t, srv, srv.eng.Home(), "Old")
	newID := seedWebSession(t, srv, srv.eng.Home(), "New")
	rows, err := srv.eng.listSessions(context.Background(), 10)
	if err != nil || len(rows) != 2 || rows[0].ID != newID {
		t.Fatalf("initial order: %+v %v", rows, err)
	}
	srv.eng.mu.Lock()
	srv.eng.prov = stalledWebProvider{}
	srv.eng.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	activityEvents := 0
	err = srv.eng.runStream(ctx, "hello again", oldID, "", func(ev wireEvent) {
		if ev.Type != "session_activity" {
			return
		}
		activityEvents++
		defer cancel() // The model never replies; activity must already be visible.
		if ev.SessionID != oldID {
			t.Errorf("activity for %q, want %q", ev.SessionID, oldID)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, localProviderRequest(http.MethodGet, "/api/sessions?limit=1"))
		var rows []sessionMeta
		if rec.Code != http.StatusOK {
			t.Errorf("sessions status=%d body=%s", rec.Code, rec.Body.String())
			return
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Error(err)
			return
		}
		if len(rows) != 1 || rows[0].ID != oldID || rows[0].MessageCount != 2 {
			t.Errorf("message must be saved before activity: %+v", rows)
			return
		}
		updated, err := time.Parse(time.RFC3339Nano, rows[0].UpdatedAt)
		if err != nil || updated.Before(time.Now().Add(-time.Minute)) {
			t.Errorf("invalid activity timestamp: %q %v", rows[0].UpdatedAt, err)
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if activityEvents != 1 {
		t.Fatalf("activity events=%d, want 1 before model reply", activityEvents)
	}
}
