package webgui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"supercli/internal/storage/session"
)

var errSessionOutsideWorkspace = errors.New("session not found in active project")

func sameSessionWorkspace(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if a == "." || b == "." {
		return false
	}
	// Resolve aliases/symlinks when both directories still exist. This is the
	// strongest portable identity check and also handles separator variants.
	if ai, errA := os.Stat(a); errA == nil {
		if bi, errB := os.Stat(b); errB == nil && os.SameFile(ai, bi) {
			return true
		}
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// sessionMeta is the browser-facing summary of one stored session.
type sessionMeta struct {
	ID              string `json:"id"`
	FirstUserMsg    string `json:"first_user_msg"`
	MessageCount    int    `json:"message_count"`
	StartedAt       string `json:"started_at"`
	Model           string `json:"model,omitempty"`
	Provider        string `json:"provider,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	RuntimeKnown    bool   `json:"runtime_known,omitempty"`
	ParentID        string `json:"parent_id,omitempty"`
}

// transcriptMsg is one message in a session transcript.
type transcriptMsg struct {
	Seq         int                  `json:"seq"`
	Role        string               `json:"role"`
	Content     string               `json:"content"`
	Attachments []string             `json:"attachments,omitempty"`
	Name        string               `json:"name,omitempty"`
	ToolCallID  string               `json:"tool_call_id,omitempty"`
	ToolCalls   []transcriptToolCall `json:"tool_calls,omitempty"`
	Turn        *transcriptTurn      `json:"turn,omitempty"`
}

type transcriptPage struct {
	Messages  []transcriptMsg `json:"messages"`
	HasMore   bool            `json:"has_more"`
	BeforeSeq int             `json:"before_seq,omitempty"`
}

type transcriptToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type transcriptTurn struct {
	ElapsedMS    int64                `json:"elapsed_ms"`
	TokIn        int64                `json:"tok_in"`
	TokOut       int64                `json:"tok_out"`
	TokTotal     int64                `json:"tok_total"`
	TokCached    int64                `json:"tok_cached,omitempty"`
	ReasoningTok int64                `json:"reasoning_tok,omitempty"`
	ToolCalls    int                  `json:"tool_calls,omitempty"`
	FileChanges  []session.FileChange `json:"file_changes,omitempty"`
}

// memoryItem is the browser-facing form of a memory entry.
type memoryItem struct {
	ID        string   `json:"id"`
	Scope     string   `json:"scope"`
	Target    string   `json:"target"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags,omitempty"`
	Source    string   `json:"source,omitempty"`
	UpdatedAt string   `json:"updated_at"`
}

// goalView is the active goal plus its tasks for the goals panel.
type goalView struct {
	ID                   string     `json:"id"`
	Title                string     `json:"title"`
	Description          string     `json:"description,omitempty"`
	SuccessCriteria      string     `json:"success_criteria,omitempty"`
	Notes                string     `json:"notes,omitempty"`
	Status               string     `json:"status"`
	VerificationStatus   string     `json:"verification_status,omitempty"`
	VerificationEvidence string     `json:"verification_evidence,omitempty"`
	VerifiedAt           string     `json:"verified_at,omitempty"`
	ReadyForVerification bool       `json:"ready_for_verification"`
	CanFinish            bool       `json:"can_finish"`
	Tasks                []taskView `json:"tasks"`
}

// taskView is one task under a goal.
type taskView struct {
	Seq    int    `json:"seq"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type goalMutation struct {
	Action          string `json:"action"`
	Title           string `json:"title,omitempty"`
	Description     string `json:"description,omitempty"`
	SuccessCriteria string `json:"success_criteria,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	TaskSeq         int    `json:"task_seq,omitempty"`
	Status          string `json:"status,omitempty"`
	Text            string `json:"text,omitempty"`
	Passed          *bool  `json:"passed,omitempty"`
}

// listSessions returns up to limit recent sessions for the active workspace,
// newest first. The shared database keeps every project's history; filtering
// changes visibility only and never deletes or migrates sessions.
// A nil store (open failure) yields an empty slice, never an error,
// so the panel degrades gracefully.
func (e *Engine) listSessions(ctx context.Context, limit int) ([]sessionMeta, error) {
	store, err := e.sessionStore()
	if err != nil {
		return []sessionMeta{}, nil
	}
	recent, err := store.ListRecentByCwd(ctx, e.Home(), limit)
	if err != nil {
		return nil, err
	}
	out := make([]sessionMeta, 0, len(recent))
	for _, r := range recent {
		title := cleanLLMSummary(r.Title)
		if title == "" {
			title = summarizeHistoryMessage(r.FirstUserMsg, defaultHistorySummaryLen)
		}
		out = append(out, sessionMeta{
			ID:              r.ID,
			FirstUserMsg:    title,
			MessageCount:    r.MessageCount,
			StartedAt:       r.StartedAt.Format(time.RFC3339),
			Model:           r.Model,
			Provider:        r.Provider,
			ReasoningEffort: r.ReasoningEffort,
			RuntimeKnown:    r.Provider != "",
			ParentID:        r.ParentID,
		})
	}
	return out, nil
}

func (e *Engine) queuedTasks(ctx context.Context) ([]session.QueuedTask, error) {
	store, err := e.sessionStore()
	if err != nil {
		return nil, err
	}
	return store.ListQueuedTasks(ctx, e.Home())
}

func (e *Engine) enqueueTask(ctx context.Context, sessionID, prompt string) (session.QueuedTask, error) {
	store, err := e.sessionStore()
	if err != nil {
		return session.QueuedTask{}, err
	}
	if sessionID != "" {
		meta, err := store.Get(sessionID)
		if err != nil || !sameSessionWorkspace(meta.Cwd, e.Home()) {
			return session.QueuedTask{}, errSessionOutsideWorkspace
		}
	}
	return store.EnqueueTask(ctx, e.Home(), sessionID, prompt)
}

func (e *Engine) deleteTask(ctx context.Context, id string) error {
	store, err := e.sessionStore()
	if err != nil {
		return err
	}
	return store.DeleteQueuedTask(ctx, e.Home(), id)
}

func (e *Engine) updateTask(ctx context.Context, id, prompt string) error {
	store, err := e.sessionStore()
	if err != nil {
		return err
	}
	return store.UpdateQueuedTask(ctx, e.Home(), id, prompt)
}

func (e *Engine) moveTask(ctx context.Context, id string, position int) error {
	store, err := e.sessionStore()
	if err != nil {
		return err
	}
	return store.MoveQueuedTask(ctx, e.Home(), id, position)
}

func (e *Engine) renameSession(id, title string) error {
	id = strings.TrimSpace(id)
	title = strings.TrimSpace(title)
	if id == "" {
		return errors.New("session id is required")
	}
	if title == "" {
		return errors.New("session title is required")
	}
	if len([]rune(title)) > 120 {
		return errors.New("session title is too long (maximum 120 characters)")
	}
	store, err := e.sessionStore()
	if err != nil {
		return err
	}
	meta, err := store.Get(id)
	if err != nil {
		return err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return errSessionOutsideWorkspace
	}
	return store.SetTitle(id, title)
}

func (e *Engine) deleteSession(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("session id is required")
	}
	store, err := e.sessionStore()
	if err != nil {
		return err
	}
	meta, err := store.Get(id)
	if err != nil {
		return err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return errSessionOutsideWorkspace
	}
	return store.Delete(id)
}

// transcript returns all messages for one session in order.
