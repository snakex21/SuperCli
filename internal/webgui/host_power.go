package webgui

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	systemhostpower "supercli/internal/system/hostpower"
)

type hostPowerAdmissionKey struct{}

var hostPowerAdmissionSequence atomic.Uint64

// A web admission is only an active-root fence, never tool authority. It
// revokes old countdowns before startup and overlaps the agent's own Begin.
// Finish(false) cannot authorize power, and release is safe on every exit.
type hostPowerAdmission struct {
	controller *systemhostpower.Controller
	id         string
	once       sync.Once
}

func (a *hostPowerAdmission) release() {
	if a != nil {
		a.once.Do(func() {
			if a.controller != nil {
				a.controller.Finish(a.id, false)
			}
		})
	}
}

func (e *Engine) admitHostPower(ctx context.Context) (context.Context, *hostPowerAdmission) {
	admission := &hostPowerAdmission{
		controller: e.hostPowerController(),
		id:         fmt.Sprintf("web-admission-%d", hostPowerAdmissionSequence.Add(1)),
	}
	if admission.controller != nil {
		admission.controller.Begin(admission.id)
	}
	return context.WithValue(ctx, hostPowerAdmissionKey{}, admission), admission
}

// hostPowerController is shared by every root conversation, never by feature
// loops. Literal Engines fail closed; only NewEngine injects the live adapter.
func (e *Engine) hostPowerController() *systemhostpower.Controller {
	if e == nil {
		return nil
	}
	e.hostPowerMu.Lock()
	defer e.hostPowerMu.Unlock()
	if e.hostPower == nil && !e.hostPowerClosed {
		e.hostPower = systemhostpower.New(systemhostpower.Options{})
	}
	return e.hostPower
}

// Closing must not lazily create a controller or leave a countdown alive while
// waiting for a run's persistence fences.
func (e *Engine) closeHostPower() {
	e.hostPowerMu.Lock()
	defer e.hostPowerMu.Unlock()
	e.hostPowerClosed = true
	if e.hostPower != nil {
		e.hostPower.Close()
	}
}

func (s *Server) handleHostPower(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.writeHostPowerSnapshot(w)
}

func (s *Server) handleHostPowerCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if controller := s.eng.hostPowerController(); controller != nil {
		controller.Cancel()
	}
	s.writeHostPowerSnapshot(w)
}

func (s *Server) writeHostPowerSnapshot(w http.ResponseWriter) {
	snapshot := systemhostpower.Snapshot{Status: "idle"}
	if controller := s.eng.hostPowerController(); controller != nil {
		snapshot = controller.Snapshot()
	}
	// Recheck/executor errors may contain paths or OS diagnostics. The polling
	// API reports only a safe summary; the controller retains its internal error.
	if snapshot.Error != "" {
		snapshot.Error = "Host power action was cancelled or could not complete safely."
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, snapshot)
}
