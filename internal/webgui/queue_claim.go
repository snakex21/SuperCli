package webgui

import (
	"context"
	"log"
	"strings"
	"time"

	"supercli/internal/storage/session"
)

// A claim fences all chat transports, including reconnects and other devices.
// Unaccepted attempts release it; accepted attempts retire the durable row while
// retaining staged attachments. Missing rows are also neutral duplicate results.
// This is an admission fence, not a crash-recovery transaction with the agent.
type queuedChatClaim struct {
	server   *Server
	store    *session.Store
	task     session.QueuedTask
	accepted bool
	retired  bool
}

func (s *Server) claimQueuedChat(ctx context.Context, req *chatRequest) (*queuedChatClaim, error) {
	s.queueClaimsMu.Lock()
	defer s.queueClaimsMu.Unlock()
	id := strings.TrimSpace(req.QueueTaskID)
	if s.queueClaims[id] != nil {
		return nil, nil
	}
	store, err := s.eng.sessionStore()
	if err != nil {
		return nil, err
	}
	tasks, err := store.ListQueuedTasks(ctx, s.eng.Home())
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if task.ID != id {
			continue
		}
		claim := &queuedChatClaim{server: s, store: store, task: task}
		if s.queueClaims == nil {
			s.queueClaims = make(map[string]*queuedChatClaim)
		}
		s.queueClaims[id] = claim
		// The durable row, not a stale phone copy, owns prompt and attachments.
		req.Prompt, req.Attachments = task.Prompt, task.Attachments
		if task.SessionID != "" {
			req.SessionID = task.SessionID
		}
		return claim, nil
	}
	return nil, nil
}

func (c *queuedChatClaim) accept() {
	if c == nil || c.accepted {
		return
	}
	c.accepted = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Do not use Engine.Home here: another client may have switched projects.
	if err := c.store.DeleteQueuedTask(ctx, c.task.Cwd, c.task.ID); err != nil {
		// Keep the admission tombstone on storage failure; never repeat a run.
		log.Printf("retire accepted queue task %s: %v", c.task.ID, err)
		return
	}
	c.retired = true
	c.server.eng.syncHub.queueChanged(c.task.Cwd)
}

func (c *queuedChatClaim) finish() {
	if c == nil {
		return
	}
	if c.accepted && !c.retired {
		return
	}
	c.server.queueClaimsMu.Lock()
	delete(c.server.queueClaims, c.task.ID)
	c.server.queueClaimsMu.Unlock()
}

func (h *syncHub) queueChanged(home string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.publishLocked(syncFrame{Type: "queue_changed", ProjectPath: home})
}

func (h *syncHub) queueRunning(home string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, run := range h.runs {
		if sameSessionWorkspace(run.info.ProjectPath, home) && !run.info.QueuePaused {
			return true
		}
	}
	return false
}
