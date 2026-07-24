package memory

import (
	"fmt"
	"strings"
	"time"
)

// ProjectCard is the global store's one-card-per-project summary
// ("wizytówka"): what the project is, where it lives, what state
// it is in, and when the last session ran. Cards power the
// session-start briefing's "other projects" list.
type ProjectCard struct {
	Key         string // ProjectKey(path)
	Name        string
	Path        string
	Description string
	State       string // e.g. "active", "paused"
	LastSession time.Time
}

// ensureCards creates the project_cards table. Called lazily by
// every card method so old databases upgrade on first use.
func (s *Store) ensureCards() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS project_cards (
		key          TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		path         TEXT NOT NULL,
		description  TEXT NOT NULL DEFAULT '',
		state        TEXT NOT NULL DEFAULT 'active',
		last_session INTEGER NOT NULL DEFAULT 0
	)`)
	return err
}

// UpsertProjectCard inserts or updates a card. Empty Description
// or State keep the previously stored value, so a session start
// can bump LastSession without erasing the description written by
// the end-of-session summary.
func (s *Store) UpsertProjectCard(c ProjectCard) error {
	if s == nil {
		return fmt.Errorf("memory.UpsertProjectCard: nil store")
	}
	if c.Key == "" || c.Path == "" {
		return fmt.Errorf("memory.UpsertProjectCard: key and path are required")
	}
	if err := s.ensureCards(); err != nil {
		return err
	}
	if c.Name == "" {
		c.Name = c.Key
	}
	if c.LastSession.IsZero() {
		c.LastSession = time.Now()
	}
	_, err := s.db.Exec(`INSERT INTO project_cards(key, name, path, description, state, last_session)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET
			name = excluded.name,
			path = excluded.path,
			description = CASE WHEN excluded.description = '' THEN project_cards.description ELSE excluded.description END,
			state = CASE WHEN excluded.state = '' THEN project_cards.state ELSE excluded.state END,
			last_session = excluded.last_session`,
		c.Key, c.Name, c.Path, strings.TrimSpace(c.Description), strings.TrimSpace(c.State), c.LastSession.Unix())
	return err
}

// GetProjectCard returns the card for a project key, or ok=false.
func (s *Store) GetProjectCard(key string) (ProjectCard, bool) {
	if s == nil {
		return ProjectCard{}, false
	}
	if err := s.ensureCards(); err != nil {
		return ProjectCard{}, false
	}
	var c ProjectCard
	var last int64
	err := s.db.QueryRow(`SELECT key, name, path, description, state, last_session FROM project_cards WHERE key = ?`, key).
		Scan(&c.Key, &c.Name, &c.Path, &c.Description, &c.State, &last)
	if err != nil {
		return ProjectCard{}, false
	}
	c.LastSession = time.Unix(last, 0)
	return c, true
}

// ListProjectCards returns cards, most recently used first.
// limit <= 0 means all.
func (s *Store) ListProjectCards(limit int) ([]ProjectCard, error) {
	if s == nil {
		return nil, nil
	}
	if err := s.ensureCards(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT key, name, path, description, state, last_session FROM project_cards ORDER BY last_session DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectCard
	for rows.Next() {
		var c ProjectCard
		var last int64
		if err := rows.Scan(&c.Key, &c.Name, &c.Path, &c.Description, &c.State, &last); err != nil {
			return nil, err
		}
		c.LastSession = time.Unix(last, 0)
		out = append(out, c)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, rows.Err()
}
