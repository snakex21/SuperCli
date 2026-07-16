package webgui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const scheduleQueuePrefix = "[NESTCAFE_SCHEDULE] "

type webSchedule struct {
	ID        string `json:"id"`
	Cron      string `json:"cron"`
	Prompt    string `json:"prompt"`
	Workspace string `json:"workspace"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	LastRunAt string `json:"last_run_at,omitempty"`
	NextRunAt string `json:"next_run_at,omitempty"`
}

type scheduleManager struct {
	mu      sync.Mutex
	path    string
	items   []webSchedule
	enqueue func(workspace, prompt string) error
	stop    chan struct{}
	done    chan struct{}
}

func newScheduleManager(dataDir string, enqueue func(workspace, prompt string) error) *scheduleManager {
	manager := &scheduleManager{
		path: filepath.Join(dataDir, "schedules.json"), enqueue: enqueue,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	_ = manager.load()
	manager.refreshNextRuns(time.Now())
	go manager.run()
	return manager
}

func (m *scheduleManager) Close() {
	if m == nil {
		return
	}
	close(m.stop)
	<-m.done
}

func (m *scheduleManager) List(workspace string) []webSchedule {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]webSchedule, 0, len(m.items))
	for _, item := range m.items {
		if workspace == "" || sameSessionWorkspace(item.Workspace, workspace) {
			out = append(out, item)
		}
	}
	return out
}

func (m *scheduleManager) Create(cron, prompt, workspace string) (webSchedule, error) {
	cron = strings.TrimSpace(cron)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || workspace == "" {
		return webSchedule{}, errors.New("prompt and workspace are required")
	}
	next, err := nextCronTime(cron, time.Now())
	if err != nil {
		return webSchedule{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	item := webSchedule{
		ID: randomScheduleID(), Cron: cron, Prompt: prompt, Workspace: workspace,
		Enabled: true, CreatedAt: now, UpdatedAt: now, NextRunAt: next.Format(time.RFC3339),
	}
	m.mu.Lock()
	m.items = append(m.items, item)
	err = m.saveLocked()
	m.mu.Unlock()
	return item, err
}

func (m *scheduleManager) SetEnabled(id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.items {
		if m.items[index].ID != id {
			continue
		}
		m.items[index].Enabled = enabled
		m.items[index].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		m.items[index].NextRunAt = ""
		if enabled {
			next, err := nextCronTime(m.items[index].Cron, time.Now())
			if err != nil {
				return err
			}
			m.items[index].NextRunAt = next.Format(time.RFC3339)
		}
		return m.saveLocked()
	}
	return errors.New("schedule not found")
}

func (m *scheduleManager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.items {
		if m.items[index].ID == id {
			m.items = append(m.items[:index], m.items[index+1:]...)
			return m.saveLocked()
		}
	}
	return errors.New("schedule not found")
}

func (m *scheduleManager) run() {
	defer close(m.done)
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	m.tick(time.Now())
	for {
		select {
		case now := <-ticker.C:
			m.tick(now)
		case <-m.stop:
			return
		}
	}
}

func (m *scheduleManager) tick(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := false
	for index := range m.items {
		item := &m.items[index]
		if !item.Enabled || item.NextRunAt == "" {
			continue
		}
		next, err := time.Parse(time.RFC3339, item.NextRunAt)
		if err != nil || now.Before(next) {
			continue
		}
		if m.enqueue != nil && m.enqueue(item.Workspace, scheduleQueuePrefix+item.Prompt) != nil {
			continue
		}
		item.LastRunAt = now.UTC().Format(time.RFC3339)
		item.UpdatedAt = item.LastRunAt
		following, nextErr := nextCronTime(item.Cron, now.Add(time.Minute))
		if nextErr != nil {
			item.Enabled = false
			item.NextRunAt = ""
		} else {
			item.NextRunAt = following.Format(time.RFC3339)
		}
		changed = true
	}
	if changed {
		_ = m.saveLocked()
	}
}

func (m *scheduleManager) refreshNextRuns(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := false
	for index := range m.items {
		item := &m.items[index]
		if !item.Enabled || item.NextRunAt != "" {
			continue
		}
		if next, err := nextCronTime(item.Cron, now); err == nil {
			item.NextRunAt = next.Format(time.RFC3339)
			changed = true
		}
	}
	if changed {
		_ = m.saveLocked()
	}
}

func (m *scheduleManager) load() error {
	data, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &m.items)
}

func (m *scheduleManager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path, data, 0o600)
}

func (s *Server) handleSchedules(w http.ResponseWriter, r *http.Request) {
	if s.eng.schedules == nil {
		http.Error(w, "scheduler unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]any{"schedules": s.eng.schedules.List(s.eng.Home())})
	case http.MethodPost:
		var body struct {
			Cron   string `json:"cron"`
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := s.eng.schedules.Create(body.Cron, body.Prompt, s.eng.Home())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, item)
	case http.MethodPatch:
		var body struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.eng.schedules.SetEnabled(strings.TrimSpace(body.ID), body.Enabled); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	case http.MethodDelete:
		if err := s.eng.schedules.Delete(strings.TrimSpace(r.URL.Query().Get("id"))); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func nextCronTime(expression string, from time.Time) (time.Time, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return time.Time{}, errors.New("cron must contain 5 fields: minute hour day month weekday")
	}
	start := from.Truncate(time.Minute).Add(time.Minute)
	for minute := 0; minute < 60*24*366; minute++ {
		candidate := start.Add(time.Duration(minute) * time.Minute)
		values := []int{candidate.Minute(), candidate.Hour(), candidate.Day(), int(candidate.Month()), int(candidate.Weekday())}
		limits := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
		matches := true
		for index := range fields {
			if !cronFieldMatches(fields[index], values[index], limits[index][0], limits[index][1]) {
				matches = false
				break
			}
		}
		if matches {
			return candidate, nil
		}
	}
	return time.Time{}, errors.New("cron has no matching time within one year")
}

func cronFieldMatches(field string, value, min, max int) bool {
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		step := 1
		if pieces := strings.SplitN(part, "/", 2); len(pieces) == 2 {
			part = pieces[0]
			parsed, err := strconv.Atoi(pieces[1])
			if err != nil || parsed <= 0 {
				return false
			}
			step = parsed
		}
		start, end := min, max
		switch {
		case part == "" || part == "*":
		case strings.Contains(part, "-"):
			pieces := strings.SplitN(part, "-", 2)
			left, leftErr := strconv.Atoi(pieces[0])
			right, rightErr := strconv.Atoi(pieces[1])
			if leftErr != nil || rightErr != nil {
				return false
			}
			start, end = left, right
		default:
			exact, err := strconv.Atoi(part)
			if err != nil {
				return false
			}
			start, end = exact, exact
		}
		if start < min || end > max || start > end {
			return false
		}
		if value >= start && value <= end && (value-start)%step == 0 {
			return true
		}
	}
	return false
}

func randomScheduleID() string {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return "schedule-" + hex.EncodeToString(raw[:])
	}
	return "schedule-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
