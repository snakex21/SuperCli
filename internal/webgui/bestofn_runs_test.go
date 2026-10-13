package webgui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/agent/darwin"
	"supercli/internal/llm"
)

func bestOfNGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@x", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

// bestOfNRepo makes a repo with two candidate branches, each adding one file.
func bestOfNRepo(t *testing.T) (string, []string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	bestOfNGit(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bestOfNGit(t, dir, "add", "a.txt")
	bestOfNGit(t, dir, "commit", "-qm", "init")
	branches := []string{"darwin-1-aa", "darwin-2-bb"}
	for i, b := range branches {
		bestOfNGit(t, dir, "checkout", "-q", "-b", b)
		name := []string{"one.txt", "two.txt"}[i]
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		bestOfNGit(t, dir, "add", name)
		bestOfNGit(t, dir, "commit", "-qm", name)
		bestOfNGit(t, dir, "checkout", "-q", "-")
	}
	return dir, branches
}

func bestOfNResult(branches []string) darwin.Result {
	r := darwin.Result{Prompt: "fix", WinnerIndex: 0, Score: 0.8, Reason: "clean", TotalUsage: llm.Usage{Total: 30}}
	for i, b := range branches {
		r.Candidates = append(r.Candidates, darwin.Candidate{
			Index: i, AgentID: "agent", Model: "m", Branch: b, Kept: true, Text: "t", Diff: "+x\n", DiffStat: " 1 file changed",
		})
	}
	r.Candidates = append(r.Candidates, darwin.Candidate{Index: len(branches), Err: errors.New("boom")})
	return r
}

func bestOfNPost(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleDarwin(rec, httptest.NewRequest(http.MethodPost, "/api/darwin", strings.NewReader(body)))
	return rec
}

func TestBestOfNRunApplyNonWinnerDeletesAllBranches(t *testing.T) {
	dir, branches := bestOfNRepo(t)
	eng := &Engine{dataDir: t.TempDir()}
	srv := &Server{eng: eng}
	id, err := eng.saveBestOfNRun(dir, bestOfNResult(branches))
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleDarwin(rec, httptest.NewRequest(http.MethodGet, "/api/darwin?id="+id, nil))
	var run bestOfNRun
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &run) != nil {
		t.Fatalf("GET status=%d body=%s", rec.Code, rec.Body)
	}
	if run.Status != "pending" || len(run.Candidates) != 3 || run.Candidates[2].Error != "boom" {
		t.Fatalf("run = %+v", run)
	}

	if rec := bestOfNPost(t, srv, `{"id":"`+id+`","action":"apply","index":2}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("apply of failed candidate: status=%d", rec.Code)
	}
	rec = bestOfNPost(t, srv, `{"id":"`+id+`","action":"apply","index":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status=%d body=%s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "two.txt")); err != nil {
		t.Fatalf("candidate #2 not applied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "one.txt")); err == nil {
		t.Fatal("candidate #1 must not be applied")
	}
	if out := strings.TrimSpace(bestOfNGit(t, dir, "branch", "--list", "darwin-*")); out != "" {
		t.Fatalf("candidate branches left: %s", out)
	}
	if rec := bestOfNPost(t, srv, `{"id":"`+id+`","action":"discard"}`); rec.Code != http.StatusConflict {
		t.Fatalf("second action must conflict, status=%d", rec.Code)
	}
	if run, err = eng.loadBestOfNRun(id); err != nil || run.Status != "applied" || run.AppliedIndex != 1 {
		t.Fatalf("stored run = %+v err=%v", run, err)
	}
}

func TestBestOfNRunDiscardAndBadIDs(t *testing.T) {
	dir, branches := bestOfNRepo(t)
	eng := &Engine{dataDir: t.TempDir()}
	srv := &Server{eng: eng}
	id, err := eng.saveBestOfNRun(dir, bestOfNResult(branches))
	if err != nil {
		t.Fatal(err)
	}
	if rec := bestOfNPost(t, srv, `{"id":"`+id+`","action":"discard"}`); rec.Code != http.StatusOK {
		t.Fatalf("discard status=%d body=%s", rec.Code, rec.Body)
	}
	if out := strings.TrimSpace(bestOfNGit(t, dir, "branch", "--list", "darwin-*")); out != "" {
		t.Fatalf("candidate branches left: %s", out)
	}
	if st := strings.TrimSpace(bestOfNGit(t, dir, "status", "--porcelain")); st != "" {
		t.Fatalf("discard touched the tree: %s", st)
	}
	for _, bad := range []string{"../x", "bon-zz", ""} {
		rec := httptest.NewRecorder()
		srv.handleDarwin(rec, httptest.NewRequest(http.MethodGet, "/api/darwin?id="+bad, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("id %q: status=%d", bad, rec.Code)
		}
	}
}
