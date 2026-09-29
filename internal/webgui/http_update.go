package webgui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"supercli/internal/system/updater"
)

type applicationUpdater interface {
	Check(context.Context) (updater.State, error)
	Download(context.Context) (updater.State, error)
	Install(context.Context) (updater.State, error)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	fail := func(status int, code string, err error) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		message := ""
		if err != nil {
			message = err.Error()
		}
		json.NewEncoder(w).Encode(map[string]string{"code": code, "error": message})
	}
	if s.runtimeAppName() != "SuperCli" {
		fail(http.StatusBadRequest, "update.unsupported", nil)
		return
	}
	action := "check"
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		var body struct {
			Action string `json:"action"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			fail(http.StatusBadRequest, "update.checkFailed", nil)
			return
		}
		action = strings.ToLower(strings.TrimSpace(body.Action))
	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if action != "check" && action != "download" && action != "install" {
		fail(http.StatusBadRequest, "update.checkFailed", nil)
		return
	}
	if action == "install" {
		// New chat streams cannot start while the executable pair is replaced.
		if !s.eng.updateGate.TryLock() {
			fail(http.StatusConflict, "update.busy", nil)
			return
		}
		defer s.eng.updateGate.Unlock()
		if s.eng.HasActiveWork() {
			fail(http.StatusConflict, "update.busy", nil)
			return
		}
	}
	var manager applicationUpdater
	var err error
	if s.updateFactory != nil {
		manager, err = s.updateFactory()
	} else {
		manager, err = updater.New()
	}
	code := map[string]string{"check": "update.checkFailed", "download": "update.downloadFailed", "install": "update.installFailed"}[action]
	if err != nil {
		fail(http.StatusInternalServerError, code, err)
		return
	}
	var state updater.State
	switch action {
	case "check":
		state, err = manager.Check(r.Context())
	case "download":
		state, err = manager.Download(r.Context())
	case "install":
		state, err = manager.Install(r.Context())
	}
	if err != nil {
		fail(http.StatusBadGateway, code, err)
		return
	}
	writeJSON(w, state)
}
