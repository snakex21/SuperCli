package webgui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	folderIndexFile          = "folder-indexing.json"
	folderIndexMaxDepth      = 2
	folderIndexMaxDirEntries = 10_000
	folderIndexMaxFiles      = 5_000
	folderIndexChunkBytes    = 12 * 1024
	folderVisionMaxBytes     = 10 << 20
)

var folderIndexMu sync.Mutex

const folderLocationsCacheTTL = 5 * time.Minute

var folderLocationsCache = struct {
	sync.Mutex
	locations []folderIndexLocation
	checkedAt time.Time
}{}

type folderIndexConfig struct {
	Enabled        bool                         `json:"enabled"`
	SelectedPaths  []string                     `json:"selected_paths"`
	CustomPaths    []string                     `json:"custom_paths"`
	VisualIndex    bool                         `json:"visual_index"`
	VisionProvider string                       `json:"vision_provider,omitempty"`
	VisionModel    string                       `json:"vision_model,omitempty"`
	OutlookIndex   bool                         `json:"outlook_index,omitempty"`
	OutlookFolder  string                       `json:"outlook_folder,omitempty"`
	OutlookMax     int                          `json:"outlook_max_messages,omitempty"`
	OutlookIndexed *folderIndexedInfo           `json:"outlook_indexed,omitempty"`
	Indexed        map[string]folderIndexedInfo `json:"indexed,omitempty"`
	LastIndexedAt  string                       `json:"last_indexed_at,omitempty"`
}

type folderIndexedInfo struct {
	FileCount        int                  `json:"file_count"`
	ContentFileCount int                  `json:"content_file_count,omitempty"`
	AIFileCount      int                  `json:"ai_file_count,omitempty"`
	ReusedFileCount  int                  `json:"reused_file_count,omitempty"`
	VisualFileCount  int                  `json:"visual_file_count,omitempty"`
	SkippedFileCount int                  `json:"skipped_file_count,omitempty"`
	VisionModel      string               `json:"vision_model,omitempty"`
	Summary          string               `json:"summary,omitempty"`
	Skipped          []folderIndexSkipped `json:"skipped,omitempty"`
	IndexedAt        string               `json:"indexed_at"`
}

type folderIndexSkipped struct {
	Path   string `json:"path"`
	Kind   string `json:"kind,omitempty"`
	Reason string `json:"reason"`
}

type folderIndexEntry struct {
	ID       string             `json:"id"`
	Label    string             `json:"label"`
	Path     string             `json:"path"`
	Kind     string             `json:"kind"`
	Selected bool               `json:"selected"`
	Indexed  *folderIndexedInfo `json:"indexed,omitempty"`
}

type folderIndexLocation struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	Kind  string `json:"kind"`
}

type folderScanCounts struct {
	PDF   int `json:"pdf"`
	DOCX  int `json:"docx"`
	XLSX  int `json:"xlsx"`
	PPTX  int `json:"pptx"`
	TXT   int `json:"txt"`
	MD    int `json:"md"`
	EML   int `json:"eml"`
	MP4   int `json:"mp4"`
	PNG   int `json:"png"`
	JPG   int `json:"jpg"`
	MP3   int `json:"mp3"`
	Other int `json:"other"`
}

type folderScanResult struct {
	Path           string               `json:"path"`
	Counts         folderScanCounts     `json:"counts"`
	Total          int                  `json:"total"`
	Files          []string             `json:"files,omitempty"`
	ContentIndexed int                  `json:"content_indexed,omitempty"`
	AIIndexed      int                  `json:"ai_indexed,omitempty"`
	Reused         int                  `json:"reused,omitempty"`
	Unsupported    int                  `json:"unsupported,omitempty"`
	AnalysisFailed int                  `json:"analysis_failed,omitempty"`
	AnalysisError  string               `json:"analysis_error,omitempty"`
	VisualIndexed  int                  `json:"visual_indexed,omitempty"`
	VisualSkipped  int                  `json:"visual_skipped,omitempty"`
	VisualError    string               `json:"visual_error,omitempty"`
	FolderSummary  string               `json:"folder_summary,omitempty"`
	SkippedTotal   int                  `json:"skipped_total,omitempty"`
	Skipped        []folderIndexSkipped `json:"skipped,omitempty"`
	Error          string               `json:"error,omitempty"`
	indexPreview   map[string]string    `json:"-"`
	visualPreview  map[string]string    `json:"-"`
}

type folderIndexJob struct {
	ID          string             `json:"id"`
	State       string             `json:"state"`
	Current     int                `json:"current"`
	Total       int                `json:"total"`
	CurrentFile string             `json:"current_file,omitempty"`
	IndexedAt   string             `json:"indexed_at,omitempty"`
	StartedAt   string             `json:"started_at"`
	FinishedAt  string             `json:"finished_at,omitempty"`
	Results     []folderScanResult `json:"results,omitempty"`
	Error       string             `json:"error,omitempty"`
}

func (s *Server) folderIndexJobSnapshot() *folderIndexJob {
	s.folderJobMu.Lock()
	defer s.folderJobMu.Unlock()
	if s.folderJob == nil {
		return nil
	}
	copy := *s.folderJob
	copy.Results = append([]folderScanResult(nil), s.folderJob.Results...)
	return &copy
}

func (s *Server) startFolderIndexJob(paths []string, config folderIndexConfig) (*folderIndexJob, bool) {
	s.folderJobMu.Lock()
	if s.folderJob != nil && s.folderJob.State == "running" {
		copy := *s.folderJob
		s.folderJobMu.Unlock()
		return &copy, false
	}
	job := &folderIndexJob{
		ID: fmt.Sprintf("folder-index-%d", time.Now().UnixNano()), State: "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.folderJob = job
	ctx, cancel := context.WithCancel(context.Background())
	s.folderJobCancel = cancel
	s.folderJobMu.Unlock()

	go func() {
		results, indexedAt, err := s.indexFolderPaths(ctx, paths, config, func(current, total int, path string) {
			s.folderJobMu.Lock()
			if s.folderJob == job {
				job.Current = current
				job.Total = total
				job.CurrentFile = path
			}
			s.folderJobMu.Unlock()
		})
		s.folderJobMu.Lock()
		defer s.folderJobMu.Unlock()
		if s.folderJob != job {
			return
		}
		s.folderJobCancel = nil
		job.Results = results
		job.IndexedAt = indexedAt
		job.CurrentFile = ""
		job.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		if errors.Is(err, context.Canceled) {
			job.State = "canceled"
			job.Error = "analiza anulowana"
		} else if err != nil {
			job.State = "failed"
			job.Error = err.Error()
		} else {
			job.State = "completed"
			job.Current = job.Total
		}
	}()
	return s.folderIndexJobSnapshot(), true
}

func (s *Server) cancelFolderIndexJob() (*folderIndexJob, bool) {
	s.folderJobMu.Lock()
	defer s.folderJobMu.Unlock()
	if s.folderJob == nil || s.folderJob.State != "running" || s.folderJobCancel == nil {
		return s.folderJob, false
	}
	s.folderJobCancel()
	copy := *s.folderJob
	return &copy, true
}
