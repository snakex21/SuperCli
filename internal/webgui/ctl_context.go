package webgui

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/llm/providers"
)

type logWriter struct{ prefix string }

func (lw logWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	if msg != "" {
		log.Printf("%s: %s", lw.prefix, msg)
	}
	return len(p), nil
}

// ensure imports used if logWriter is the only consumer of fmt/log.
var _ = fmt.Sprintf

func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	loop, err := s.eng.newLoop()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	report := loop.ContextReport()
	writeJSON(w, map[string]any{"text": agent.FormatContextReport(report), "report": report})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// cachedModelCount returns how many models are already known (cached)
// across all configured providers, without triggering a scan.
func cachedModelCount(m *providers.Manager, caps *llm.CapabilityRegistry) int {
	n := 0
	for _, p := range m.ListConfigured(caps) {
		n += len(p.Models)
	}
	return n
}
