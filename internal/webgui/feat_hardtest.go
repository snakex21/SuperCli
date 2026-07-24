package webgui

import (
	"context"
	"net/http"
	"supercli/internal/verification/hardtest"
	"time"
)

func (s *Server) handleHardTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	out, err := hardtest.Run(ctx, s.eng.Home())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, out)
}
