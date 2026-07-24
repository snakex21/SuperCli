package webgui

import (
	"net/http"
	"strings"

	"supercli/internal/tools"
)

const (
	defaultSkillPage = 50
	maxSkillPage     = 100
)

type skillView struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Risk        string   `json:"risk,omitempty"`
	Source      string   `json:"source"`
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := queryInt(r, "limit", defaultSkillPage)
	if limit > maxSkillPage {
		limit = maxSkillPage
	}
	offset := queryInt(r, "offset", 0)
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	itemsPage, total, err := s.eng.skillDiscovererFor(s.eng.Home()).List(query, offset, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if offset > total {
		offset = total
	}
	items := make([]skillView, 0, len(itemsPage))
	for _, skill := range itemsPage {
		items = append(items, skillView{
			Name: skill.Name, Description: skill.Description,
			Category: skill.Category, Tags: skill.Tags, Risk: skill.Risk,
			Source: skillSource(skill),
		})
	}
	writeJSON(w, map[string]any{
		"items": items, "total": total, "offset": offset, "limit": limit,
	})
}

func skillSource(skill tools.Skill) string {
	if source := strings.TrimSpace(skill.Source); source != "" {
		return source
	}
	switch {
	case skill.Priority >= 90:
		return "project"
	case skill.Priority >= 40:
		return "user"
	default:
		return "builtin"
	}
}
