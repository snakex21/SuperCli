package webgui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"supercli/internal/agent/darwin"
)

// Darwin (best-of-N) runs finished in the GUI are stored as small JSON files
// so the chat card can show every candidate and let the user apply any of
// them, even after a reload. Kept candidate branches stay in git until the
// user applies one or discards them all.

const bestOfNDiffCap = 256 << 10

var (
	bestOfNIDRE = regexp.MustCompile(`^bon-[0-9a-f]{12}$`)
	bestOfNMu   sync.Mutex
)

type bestOfNCandidate struct {
	Index         int    `json:"index"`
	AgentID       string `json:"agent_id"`
	Model         string `json:"model,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Kept          bool   `json:"kept"`
	Text          string `json:"text"`
	Diff          string `json:"diff,omitempty"`
	DiffTruncated bool   `json:"diff_truncated,omitempty"`
	DiffStat      string `json:"diffstat,omitempty"`
	Tokens        int    `json:"tokens"`
	Steps         int    `json:"steps,omitempty"`
	Error         string `json:"error,omitempty"`
}

type bestOfNRun struct {
	ID           string             `json:"id"`
	Home         string             `json:"home"`
	Prompt       string             `json:"prompt"`
	Created      time.Time          `json:"created"`
	WinnerIndex  int                `json:"winner_index"`
	Score        float64            `json:"score"`
	Reason       string             `json:"reason,omitempty"`
	Note         string             `json:"note,omitempty"`
	TotalTokens  int                `json:"total_tokens"`
	Status       string             `json:"status"` // pending | applied | discarded
	AppliedIndex int                `json:"applied_index"`
	Candidates   []bestOfNCandidate `json:"candidates"`
}

func (e *Engine) bestOfNDir() string {
	if e == nil || e.dataDir == "" {
		return ""
	}
	return filepath.Join(e.dataDir, "darwin-runs")
}

// saveBestOfNRun is the DarwinTool result sink: it records r for the card
// and returns the run id the tool result carries.
func (e *Engine) saveBestOfNRun(home string, r darwin.Result) (string, error) {
	dir := e.bestOfNDir()
	if dir == "" {
		return "", errors.New("darwin runs: no data dir")
	}
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	run := bestOfNRun{
		ID:           "bon-" + hex.EncodeToString(raw[:]),
		Home:         home,
		Prompt:       r.Prompt,
		Created:      time.Now().UTC(),
		WinnerIndex:  r.WinnerIndex,
		Score:        r.Score,
		Reason:       r.Reason,
		Note:         r.Note,
		TotalTokens:  r.TotalUsage.Total,
		Status:       "pending",
		AppliedIndex: -1,
	}
	var kept []string
	for _, c := range r.Candidates {
		bc := bestOfNCandidate{
			Index: c.Index, AgentID: c.AgentID, Model: c.Model, Branch: c.Branch, Kept: c.Kept,
			Text: c.Text, DiffStat: c.DiffStat, Tokens: c.Usage.Total, Steps: c.Steps,
		}
		bc.Diff = c.Diff
		if len(bc.Diff) > bestOfNDiffCap {
			bc.Diff = strings.ToValidUTF8(bc.Diff[:bestOfNDiffCap], "")
			bc.DiffTruncated = true
		}
		if c.Err != nil {
			bc.Error = c.Err.Error()
		}
		if c.Kept {
			kept = append(kept, c.Branch)
		}
		run.Candidates = append(run.Candidates, bc)
	}
	if r.Merged {
		// Auto-merge already applied the winner; nothing is left to choose.
		_ = darwin.DeleteBranches(context.Background(), home, kept)
		for i := range run.Candidates {
			run.Candidates[i].Kept = false
		}
		run.Status = "applied"
		if r.Winner != nil {
			run.AppliedIndex = r.Winner.Index
		}
	}
	bestOfNMu.Lock()
	defer bestOfNMu.Unlock()
	if err := writeBestOfNRun(dir, run); err != nil {
		return "", err
	}
	return run.ID, nil
}

func writeBestOfNRun(dir string, run bestOfNRun) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(run)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, run.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (e *Engine) loadBestOfNRun(id string) (bestOfNRun, error) {
	var run bestOfNRun
	dir := e.bestOfNDir()
	if dir == "" || !bestOfNIDRE.MatchString(id) {
		return run, os.ErrNotExist
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return run, err
	}
	err = json.Unmarshal(data, &run)
	return run, err
}

// handleDarwin serves the GUI Darwin card: GET ?id= returns a run; POST
// {id, action: "apply"|"discard", index} merges one kept candidate into the
// current branch (then deletes every kept branch) or deletes them all.
func (s *Server) handleDarwin(w http.ResponseWriter, r *http.Request) {
	if s.eng == nil {
		http.Error(w, "engine unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		run, err := s.eng.loadBestOfNRun(strings.TrimSpace(r.URL.Query().Get("id")))
		if err != nil {
			http.Error(w, "darwin run not found", http.StatusNotFound)
			return
		}
		writeJSON(w, run)
	case http.MethodPost:
		var body struct {
			ID     string `json:"id"`
			Action string `json:"action"`
			Index  int    `json:"index"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		run, status, err := s.eng.bestOfNAction(r.Context(), body.ID, body.Action, body.Index)
		if err != nil {
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, run)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (e *Engine) bestOfNAction(ctx context.Context, id, action string, index int) (bestOfNRun, int, error) {
	bestOfNMu.Lock()
	defer bestOfNMu.Unlock()
	run, err := e.loadBestOfNRun(strings.TrimSpace(id))
	if err != nil {
		return run, http.StatusNotFound, errors.New("darwin run not found")
	}
	if run.Status != "pending" {
		return run, http.StatusConflict, fmt.Errorf("darwin run already %s", run.Status)
	}
	var kept []string
	for _, c := range run.Candidates {
		if c.Kept {
			kept = append(kept, c.Branch)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	switch action {
	case "apply":
		pos := -1
		for i, c := range run.Candidates {
			if c.Index == index {
				pos = i
			}
		}
		if pos < 0 || !run.Candidates[pos].Kept {
			return run, http.StatusBadRequest, errors.New("candidate " + strconv.Itoa(index+1) + " has no kept changes")
		}
		if err := darwin.ApplyBranch(ctx, run.Home, run.Candidates[pos].Branch); err != nil {
			return run, http.StatusConflict, err
		}
		run.Status = "applied"
		run.AppliedIndex = index
	case "discard":
		run.Status = "discarded"
	default:
		return run, http.StatusBadRequest, errors.New("unknown action")
	}
	cleanupErr := darwin.DeleteBranches(ctx, run.Home, kept)
	for i := range run.Candidates {
		run.Candidates[i].Kept = false
	}
	if err := writeBestOfNRun(e.bestOfNDir(), run); err != nil {
		return run, http.StatusInternalServerError, err
	}
	if cleanupErr != nil {
		run.Note = strings.TrimSpace(run.Note + " " + cleanupErr.Error())
	}
	return run, http.StatusOK, nil
}
