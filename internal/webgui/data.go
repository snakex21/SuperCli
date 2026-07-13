package webgui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"supercli/internal/storage/goal"
	"supercli/internal/storage/memory"
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
	Seq     int             `json:"seq"`
	Role    string          `json:"role"`
	Content string          `json:"content"`
	Name    string          `json:"name,omitempty"`
	Turn    *transcriptTurn `json:"turn,omitempty"`
}

type transcriptTurn struct {
	ElapsedMS    int64 `json:"elapsed_ms"`
	TokIn        int64 `json:"tok_in"`
	TokOut       int64 `json:"tok_out"`
	TokTotal     int64 `json:"tok_total"`
	TokCached    int64 `json:"tok_cached,omitempty"`
	ReasoningTok int64 `json:"reasoning_tok,omitempty"`
	ToolCalls    int   `json:"tool_calls,omitempty"`
}

// memoryItem is the browser-facing form of a memory entry.
type memoryItem struct {
	ID        string   `json:"id"`
	Scope     string   `json:"scope"`
	Content   string   `json:"content"`
	Tags      []string `json:"tags,omitempty"`
	Source    string   `json:"source,omitempty"`
	UpdatedAt string   `json:"updated_at"`
}

// goalView is the active goal plus its tasks for the goals panel.
type goalView struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Status string     `json:"status"`
	Tasks  []taskView `json:"tasks"`
}

// taskView is one task under a goal.
type taskView struct {
	Seq    int    `json:"seq"`
	Title  string `json:"title"`
	Status string `json:"status"`
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
		title := ""
		meta, metaErr := store.Get(r.ID)
		if metaErr == nil {
			title = cleanLLMSummary(meta.Title)
		}
		if title == "" {
			title = summarizeHistoryMessage(r.FirstUserMsg, defaultHistorySummaryLen)
		}
		out = append(out, sessionMeta{
			ID:              r.ID,
			FirstUserMsg:    title,
			MessageCount:    r.MessageCount,
			StartedAt:       r.StartedAt.Format(time.RFC3339),
			Model:           meta.Model,
			Provider:        meta.Provider,
			ReasoningEffort: meta.ReasoningEffort,
			RuntimeKnown:    meta.Provider != "",
			ParentID:        meta.ParentID,
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

func (e *Engine) moveTask(ctx context.Context, id string, position int) error {
	store, err := e.sessionStore()
	if err != nil {
		return err
	}
	return store.MoveQueuedTask(ctx, e.Home(), id, position)
}

func (e *Engine) forkSession(ctx context.Context, id string, seq int, provider, model, reasoning string) (sessionMeta, error) {
	store, err := e.sessionStore()
	if err != nil {
		return sessionMeta{}, err
	}
	meta, err := store.Get(id)
	if err != nil {
		return sessionMeta{}, err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return sessionMeta{}, errSessionOutsideWorkspace
	}
	if strings.TrimSpace(provider) == "" && strings.TrimSpace(model) != "" {
		if info, ok := e.caps.Get(strings.TrimSpace(model)); ok {
			provider = info.Provider
		}
	}
	child, err := store.Fork(ctx, id, seq, provider, model, reasoning)
	if err != nil {
		return sessionMeta{}, err
	}
	return sessionMeta{ID: child.ID, FirstUserMsg: child.Title, MessageCount: child.MessageCount, StartedAt: child.CreatedAt.Format(time.RFC3339), Model: child.Model, Provider: child.Provider, ReasoningEffort: child.ReasoningEffort, RuntimeKnown: child.Provider != "", ParentID: child.ParentID}, nil
}

func (e *Engine) branchSessions(ctx context.Context, id string) ([]sessionMeta, error) {
	store, err := e.sessionStore()
	if err != nil {
		return nil, err
	}
	meta, err := store.Get(id)
	if err != nil {
		return nil, err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return nil, errSessionOutsideWorkspace
	}
	root := meta.ID
	if meta.ParentID != "" {
		root = meta.ParentID
	}
	all := []session.Session{}
	rootMeta, err := store.Get(root)
	if err == nil {
		all = append(all, rootMeta)
	}
	children, err := store.Children(ctx, root)
	if err != nil {
		return nil, err
	}
	all = append(all, children...)
	out := make([]sessionMeta, 0, len(all))
	for _, v := range all {
		out = append(out, sessionMeta{ID: v.ID, FirstUserMsg: v.Title, MessageCount: v.MessageCount, StartedAt: v.CreatedAt.Format(time.RFC3339), Model: v.Model, Provider: v.Provider, ReasoningEffort: v.ReasoningEffort, RuntimeKnown: v.Provider != "", ParentID: v.ParentID})
	}
	return out, nil
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
func (e *Engine) transcript(ctx context.Context, id string) ([]transcriptMsg, error) {
	store, err := e.sessionStore()
	if err != nil {
		return []transcriptMsg{}, nil
	}
	meta, err := store.Get(id)
	if err != nil {
		return nil, err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return nil, errSessionOutsideWorkspace
	}
	rows, err := store.ReadMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	turnRows, err := store.ReadTurnSummaries(ctx, id)
	if err != nil {
		return nil, err
	}
	turns := make(map[int]session.TurnSummary, len(turnRows))
	for _, turn := range turnRows {
		turns[turn.AssistantSeq] = turn
	}
	out := make([]transcriptMsg, 0, len(rows))
	for _, m := range rows {
		msg, err := m.ToMessage()
		if err != nil {
			return nil, err
		}
		textOnly := msg.TextOnly()
		item := transcriptMsg{
			Seq:     m.Seq,
			Role:    m.Role,
			Content: textOnly.Content,
			Name:    m.Name,
		}
		if turn, ok := turns[m.Seq]; ok {
			item.Turn = &transcriptTurn{
				ElapsedMS: turn.DurationMS, TokIn: turn.Input, TokOut: turn.Output,
				TokTotal: turn.Input + turn.Output, TokCached: turn.CachedInput,
				ReasoningTok: turn.Reasoning, ToolCalls: turn.ToolCalls,
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// memoryList returns recent memory entries across both scopes
// (project + global). A scope filter of "" returns everything.
func (e *Engine) memoryList(scope string, limit int) ([]memoryItem, error) {
	out := []memoryItem{}
	if gs, err := memory.OpenStore(e.dataDir); err == nil {
		defer gs.Close()
		if entries, err := gs.List(scope, limit); err == nil {
			out = append(out, toMemoryItems(entries)...)
		}
	}
	if ps, err := memory.OpenProjectStore(e.dataDir, e.Home()); err == nil {
		defer ps.Close()
		if entries, err := ps.List(scope, limit); err == nil {
			out = append(out, toMemoryItems(entries)...)
		}
	}
	return out, nil
}

// toMemoryItems converts store entries to the wire form.
func toMemoryItems(entries []memory.Entry) []memoryItem {
	out := make([]memoryItem, 0, len(entries))
	for _, en := range entries {
		out = append(out, memoryItem{
			ID:        en.ID,
			Scope:     en.Scope,
			Content:   en.Content,
			Tags:      en.Tags,
			Source:    en.Source,
			UpdatedAt: en.UpdatedAt.Format(time.RFC3339),
		})
	}
	return out
}

// activeGoal returns the current goal and its tasks, or nil when no
// goal is set. Open/refresh failures degrade to nil so the panel
// simply shows "no active goal".
func (e *Engine) activeGoal(ctx context.Context) (*goalView, error) {
	db, err := openDataDB(e.dataDir)
	if err != nil {
		return nil, nil
	}
	defer db.Close()
	gs := goal.NewStorage(db)
	if err := gs.Migrate(ctx); err != nil {
		return nil, nil
	}
	svc := goal.NewService(gs)
	if _, err := svc.Refresh(ctx); err != nil {
		return nil, nil
	}
	g := svc.Active()
	if g == nil {
		return nil, nil
	}
	tasks, _ := svc.ListTasks(ctx, g.ID)
	tv := make([]taskView, 0, len(tasks))
	for _, t := range tasks {
		tv = append(tv, taskView{Seq: t.Seq, Title: t.Title, Status: string(t.Status)})
	}
	return &goalView{
		ID:     g.ID,
		Title:  g.Title,
		Status: string(g.Status),
		Tasks:  tv,
	}, nil
}
