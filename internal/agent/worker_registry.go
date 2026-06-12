package agent

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Worker is a persistent child loop created by the task tool. It keeps its
// own message history so the coordinator can continue it with send_message
// instead of dragging that context into the main chat.
type Worker struct {
	ID          string
	Agent       string
	Description string
	Loop        *Loop
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Status      string
	LastResult  string
	LastError   string

	mu sync.Mutex
}

// WorkerRegistry stores live/completed workers for the current process.
type WorkerRegistry struct {
	mu      sync.RWMutex
	seq     atomic.Uint64
	workers map[string]*Worker
}

func NewWorkerRegistry() *WorkerRegistry {
	return &WorkerRegistry{workers: make(map[string]*Worker)}
}

func (r *WorkerRegistry) Add(agentName, description string, loop *Loop) *Worker {
	if r == nil {
		return nil
	}
	id := fmt.Sprintf("worker-%d", r.seq.Add(1))
	now := time.Now()
	w := &Worker{
		ID:          id,
		Agent:       agentName,
		Description: description,
		Loop:        loop,
		CreatedAt:   now,
		UpdatedAt:   now,
		Status:      "created",
	}
	r.mu.Lock()
	r.workers[id] = w
	r.mu.Unlock()
	return w
}

func (r *WorkerRegistry) Get(id string) (*Worker, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	w, ok := r.workers[id]
	return w, ok
}
