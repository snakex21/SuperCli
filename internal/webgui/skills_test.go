package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools"
)

func TestSkillsListsMetadataWithoutContent(t *testing.T) {
	project := t.TempDir()
	dataDir := t.TempDir()
	skillDir := filepath.Join(project, "skills", "project-writer")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: project-writer\ndescription: Writes project notes\n" +
		"tags: writing, notes\n---\n# Secret body\nDo not return this content."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := &Engine{
		home: project, dataDir: dataDir,
		skillCatalog: make(map[string]*tools.Discoverer),
	}
	req := httptest.NewRequest(http.MethodGet, "/api/skills?q=project-writer&limit=10", nil)
	rec := httptest.NewRecorder()
	NewServer(eng, false).handleSkills(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Items []skillView `json:"items"`
		Total int         `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 1 || len(response.Items) != 1 {
		t.Fatalf("response = %+v", response)
	}
	item := response.Items[0]
	if item.Name != "project-writer" || item.Source != "project" {
		t.Fatalf("item = %+v", item)
	}
	if rec.Body.String() == "" || containsSkillBody(rec.Body.String()) {
		t.Fatalf("skill body leaked: %s", rec.Body.String())
	}
}

func containsSkillBody(body string) bool {
	return strings.Contains(body, "Secret body") || strings.Contains(body, "Do not return")
}
