package webgui

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/storage/memory"
	"supercli/internal/tools"
)

func writeFolderIndexDocx(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	part, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	document := `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`
	if _, err := part.Write([]byte(document)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFolderIndexingPersistsSelectionAndSearchableManifest(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	documents := filepath.Join(t.TempDir(), "Documents")
	if err := os.MkdirAll(filepath.Join(documents, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(documents, "project", "roadmap.md"), []byte("# Aurora roadmap\nRelease in October."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(documents, "invoice.pdf"), []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	server := NewServer(eng, false)

	saveBody, _ := json.Marshal(map[string]any{
		"action":         "save",
		"selected_paths": []string{documents},
		"custom_paths":   []string{documents},
		"vision_model":   "echo-test",
	})
	rec := httptest.NewRecorder()
	server.handleFolderIndexing(rec, httptest.NewRequest(http.MethodPost, "/api/folder-indexing", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save status %d: %s", rec.Code, rec.Body.String())
	}

	indexBody, _ := json.Marshal(map[string]any{"action": "index", "paths": []string{documents}})
	rec = httptest.NewRecorder()
	server.handleFolderIndexing(rec, httptest.NewRequest(http.MethodPost, "/api/folder-indexing", bytes.NewReader(indexBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("index status %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Results []folderScanResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].Total != 2 || response.Results[0].Counts.PDF != 1 || response.Results[0].Counts.MD != 1 {
		t.Fatalf("scan result = %+v", response.Results)
	}

	store, err := memory.OpenProjectStore(dataDir, home)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := store.Search("Aurora October roadmap", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("indexed text preview was not searchable in project memory")
	}
	config := loadFolderIndexConfig(dataDir)
	if !config.Enabled || len(config.SelectedPaths) != 1 || config.Indexed[documents].FileCount != 2 {
		t.Fatalf("persisted config = %+v", config)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	removeBody := []byte(`{"action":"save","selected_paths":[],"custom_paths":[]}`)
	rec = httptest.NewRecorder()
	server.handleFolderIndexing(rec, httptest.NewRequest(http.MethodPost, "/api/folder-indexing", bytes.NewReader(removeBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("remove status %d: %s", rec.Code, rec.Body.String())
	}
	config = loadFolderIndexConfig(dataDir)
	if config.Enabled || len(config.SelectedPaths) != 0 || len(config.CustomPaths) != 0 || len(config.Indexed) != 0 {
		t.Fatalf("folder removal did not clear config: %+v", config)
	}
	store, err = memory.OpenProjectStore(dataDir, home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hits, err = store.Search("Aurora October roadmap", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("removed folder index remains searchable: %+v", hits)
	}
}

func TestFolderVisualIndexUsesSelectedVisionModel(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	documents := t.TempDir()
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"faktura.png", "paragon.png"} {
		if err := os.WriteFile(filepath.Join(documents, name), png, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if echo, ok := llm.Unwrap(eng.prov).(*llm.EchoProvider); ok {
		echo.SetVision(true)
	} else {
		t.Fatalf("active provider is not echo: %T", llm.Unwrap(eng.prov))
	}
	server := NewServer(eng, false)

	saveBody, _ := json.Marshal(map[string]any{
		"action": "save", "selected_paths": []string{documents}, "custom_paths": []string{documents},
		// Older clients may still send the removed limit field. It must not cap
		// indexing; NestCafe now processes every supported image in the folder.
		"visual_index": true, "vision_model": "echo-test", "vision_max_images": 1,
	})
	rec := httptest.NewRecorder()
	server.handleFolderIndexing(rec, httptest.NewRequest(http.MethodPost, "/api/folder-indexing", bytes.NewReader(saveBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save status %d: %s", rec.Code, rec.Body.String())
	}
	indexBody, _ := json.Marshal(map[string]any{"action": "index", "paths": []string{documents}})
	rec = httptest.NewRecorder()
	server.handleFolderIndexing(rec, httptest.NewRequest(http.MethodPost, "/api/folder-indexing", bytes.NewReader(indexBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("index status %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Results []folderScanResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].VisualIndexed != 2 || response.Results[0].VisualSkipped != 0 || response.Results[0].VisualError != "" {
		t.Fatalf("visual index result = %+v", response.Results)
	}
	indexed := loadFolderIndexConfig(dataDir).Indexed[documents]
	if indexed.VisualFileCount != 2 || indexed.VisionModel != "echo-test" {
		t.Fatalf("visual index metadata = %+v", indexed)
	}
}

func TestFolderIndexExtractsWordContentAndReusesCache(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	documents := t.TempDir()
	docx := filepath.Join(documents, "umowa.docx")
	writeFolderIndexDocx(t, docx, "Umowa Aurora podpisana przez Annę Kowalską 12 października 2026.")
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	server := NewServer(eng, false)
	config := defaultFolderIndexConfig()
	config.SelectedPaths = []string{documents}
	config.CustomPaths = []string{documents}
	config.VisualIndex = true
	config.VisionModel = "echo-test"

	results, _, err := server.indexFolderPaths(context.Background(), []string{documents}, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ContentIndexed != 1 || results[0].Reused != 0 {
		t.Fatalf("first index result = %+v", results)
	}
	store, err := memory.OpenProjectStore(dataDir, home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hits, err := store.Search("Aurora", 5)
	if err != nil || len(hits) == 0 {
		entries, _ := store.List("", 10)
		t.Fatalf("Word content is not searchable: hits=%+v entries=%+v err=%v", hits, entries, err)
	}

	results, _, err = server.indexFolderPaths(context.Background(), []string{documents}, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Reused != 1 || results[0].ContentIndexed != 1 {
		t.Fatalf("cached index result = %+v", results)
	}
	cache := loadFolderIndexCache(dataDir)
	if len(cache.Files) != 1 || cache.Files[folderCacheKey(docx)].Preview == "" {
		t.Fatalf("cache = %+v", cache)
	}
}

func TestFolderIndexCanRunInBackground(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	documents := t.TempDir()
	if err := os.WriteFile(filepath.Join(documents, "notatka.txt"), []byte("Projekt Kobalt ma termin w listopadzie."), 0o644); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	server := NewServer(eng, false)
	config := defaultFolderIndexConfig()
	config.SelectedPaths = []string{documents}
	config.VisualIndex = true
	config.VisionModel = "echo-test"
	job, started := server.startFolderIndexJob([]string{documents}, config)
	if !started || job == nil || job.State != "running" {
		t.Fatalf("job start = %+v started=%v", job, started)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job = server.folderIndexJobSnapshot()
		if job != nil && job.State != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job == nil || job.State != "completed" || job.Total != 1 || job.Current != 1 || len(job.Results) != 1 {
		t.Fatalf("completed job = %+v", job)
	}
}

func TestOutlookMessageContentIsSearchableAndCached(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	server := NewServer(eng, false)
	config := defaultFolderIndexConfig()
	config.OutlookIndex = true
	config.OutlookFolder = "Inbox"
	config.VisualIndex = true
	config.VisionModel = "echo-test"
	message := tools.OutlookIndexMessage{
		EntryID: "ABC-123", Folder: "Inbox", Subject: "Projekt Bursztyn",
		Sender: "Jan Nowak", SenderAddress: "jan@example.com",
		ReceivedAt: time.Date(2026, 7, 18, 10, 30, 0, 0, time.UTC),
		ModifiedAt: time.Date(2026, 7, 18, 10, 31, 0, 0, time.UTC),
		Body:       "Termin spotkania został przesunięty na piątek.", AttachmentNames: []string{"harmonogram.docx"},
	}
	cache := defaultFolderIndexCache()
	result := folderScanResult{Path: "Outlook: Inbox", Total: 1}
	server.addOutlookIndexPreviews(context.Background(), &result, []tools.OutlookIndexMessage{message}, config, &cache, nil)
	if result.ContentIndexed != 1 || result.Reused != 0 || len(result.Files) != 1 {
		t.Fatalf("Outlook result = %+v", result)
	}
	store, err := memory.OpenProjectStore(dataDir, home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := saveFolderManifest(store, result); err != nil {
		t.Fatal(err)
	}
	hits, err := store.Search("Bursztyn", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("Outlook content is not searchable: hits=%+v err=%v", hits, err)
	}
	second := folderScanResult{Path: "Outlook: Inbox", Total: 1}
	server.addOutlookIndexPreviews(context.Background(), &second, []tools.OutlookIndexMessage{message}, config, &cache, nil)
	if second.Reused != 1 || second.ContentIndexed != 1 {
		t.Fatalf("Outlook cache result = %+v", second)
	}
}

func TestFolderIndexRequiresSelectedAIModel(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	documents := t.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	server := NewServer(eng, false)
	body, _ := json.Marshal(map[string]any{"action": "index", "paths": []string{documents}})
	recorder := httptest.NewRecorder()
	server.handleFolderIndexing(recorder, httptest.NewRequest(http.MethodPost, "/api/folder-indexing", bytes.NewReader(body)))
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "wybierz model AI") {
		t.Fatalf("missing model response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestImageIndexDoesNotPreflightVisionCapability(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	documents := t.TempDir()
	if err := os.WriteFile(filepath.Join(documents, "opis.txt"), []byte("Folder zawiera materiały projektu Fiołek."), 0o644); err != nil {
		t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(documents, "zdjecie.png")
	if err := os.WriteFile(imagePath, png, 0o644); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	server := NewServer(eng, false)
	config := defaultFolderIndexConfig()
	config.SelectedPaths = []string{documents}
	config.VisualIndex = true
	config.VisionModel = "echo-test"
	results, _, err := server.indexFolderPaths(context.Background(), []string{documents}, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	result := results[0]
	if result.AIIndexed != 2 || result.VisualIndexed != 1 || result.VisualSkipped != 0 || result.SkippedTotal != 0 {
		t.Fatalf("image should be attempted regardless of capability metadata: %+v", result)
	}
	if len(result.Skipped) != 0 {
		t.Fatalf("image was rejected before the provider call: %+v (path %s)", result.Skipped, imagePath)
	}
	if strings.TrimSpace(result.FolderSummary) == "" {
		t.Fatal("AI folder note was not created")
	}
}

func TestCancelFolderIndexJobSignalsRunningAnalysis(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{
		folderJob:       &folderIndexJob{ID: "active", State: "running"},
		folderJobCancel: cancel,
	}
	job, ok := server.cancelFolderIndexJob()
	if !ok || job == nil || job.ID != "active" {
		t.Fatalf("cancel result = (%+v, %v)", job, ok)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("running folder analysis did not receive cancellation")
	}
}

func TestWebLoopRegistersWorkingMemoryTools(t *testing.T) {
	dataDir := t.TempDir()
	home := t.TempDir()
	eng, err := NewEngine(echoConfig(), home, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if _, err := eng.newLoop(); err != nil {
		t.Fatal(err)
	}
	eng.diagnosticMu.RLock()
	registry := eng.diagnosticRegistry
	eng.diagnosticMu.RUnlock()
	if registry == nil {
		t.Fatal("web loop did not publish its tool registry")
	}
	if _, ok := registry.Get("remember"); !ok {
		t.Fatal("remember tool is missing from the web loop")
	}
	if _, ok := registry.Get("recall"); !ok {
		t.Fatal("recall tool is missing from the web loop")
	}
	result, err := registry.Execute(context.Background(), "remember", json.RawMessage(`{"text":"Użytkownik ma na imię Maks.","topic":"user_profile"}`))
	if err != nil || result.Err != nil {
		t.Fatalf("remember execute: result=%+v err=%v", result, err)
	}
	recallResult, err := registry.Execute(context.Background(), "recall", json.RawMessage(`{"query":"user name"}`))
	if err != nil || recallResult.Err != nil {
		t.Fatalf("recall execute: result=%+v err=%v", recallResult, err)
	}
	if !strings.Contains(recallResult.Text, "Maks") {
		t.Fatalf("cross-language recall missed the saved name: %s", recallResult.Text)
	}
	global, err := memory.OpenStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer global.Close()
	hits, err := global.Search("Maks", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("remember did not persist: hits=%+v err=%v", hits, err)
	}
}
