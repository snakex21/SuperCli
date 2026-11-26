package agent

import (
	"strings"
	"sync"
)

// Worker reports the model has not been asked about, by name (after 1.0.41).
//
// In the GUI every run has its own loop; a worker started by an earlier run
// delivers its report through that run's (detached) loop. When the report
// lands while a newer run of the conversation is still writing its reply, the
// report is saved before that reply. The next turn then read "reports are the
// task-notification messages above", saw its own later answer below them and
// told the user that nothing new had arrived (audited: four reports missed for
// almost five hours). The next request now names those reports explicitly.

// WorkerReportRef names one delivered worker report.
type WorkerReportRef struct {
	ID     string
	Status string
	Title  string
}

type unseenReportList struct {
	mu   sync.Mutex
	refs []WorkerReportRef
}

func (u *unseenReportList) add(ref WorkerReportRef) {
	if ref.ID == "" {
		return
	}
	u.mu.Lock()
	u.refs = append(u.refs, ref)
	u.mu.Unlock()
}

func (u *unseenReportList) take() []WorkerReportRef {
	u.mu.Lock()
	defer u.mu.Unlock()
	refs := u.refs
	u.refs = nil
	return refs
}

// workerReportRef reads the worker id, status and summary of a report.
func workerReportRef(content string) WorkerReportRef {
	return WorkerReportRef{ID: notificationTag(content, "task-id"), Status: notificationTag(content, "status"), Title: clipRunes(strings.Join(strings.Fields(notificationTag(content, "summary")), " "), 90)}
}

func notificationTag(content, tag string) string {
	_, rest, ok := strings.Cut(content, "<"+tag+">")
	if !ok {
		return ""
	}
	value, _, ok := strings.Cut(rest, "</"+tag+">")
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// TakeUnseenBackgroundReports returns the reports this loop saved that no
// request has carried yet and acknowledges them (like
// AcknowledgeBackgroundReports). A host hands them to the next run with
// NoteUnseenWorkerReports.
func (l *Loop) TakeUnseenBackgroundReports() []WorkerReportRef {
	refs := l.unseenReports.take()
	if l.unseenNotices.Swap(0) != 0 {
		l.emitBackgroundAgents()
	}
	return refs
}

// NoteUnseenWorkerReports names reports this run's model has not seen yet:
// they are in the loaded history, possibly above its previous answer. The
// next coordinator request lists them (request-only, never persisted).
func (l *Loop) NoteUnseenWorkerReports(refs []WorkerReportRef) {
	l.carriedReportsMu.Lock()
	l.carriedReports = append(l.carriedReports, refs...)
	l.carriedReportsMu.Unlock()
}

// unseenReportsHint lists the carried reports once and forgets them.
func (l *Loop) unseenReportsHint() string {
	l.carriedReportsMu.Lock()
	refs := l.carriedReports
	l.carriedReports = nil
	l.carriedReportsMu.Unlock()
	if len(refs) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var parts []string
	for _, ref := range refs {
		key := ref.ID + "\x00" + ref.Status
		if seen[key] {
			continue
		}
		seen[key] = true
		item := ref.ID
		if ref.Title != "" {
			item += " (" + ref.Title + ")"
		}
		if ref.Status != "" {
			item += " — " + ref.Status
		}
		parts = append(parts, item)
	}
	return "\nNew worker reports since your last reply (unread, even if they appear above your previous answer: they arrived while it was being written): " +
		strings.Join(parts, "; ") + ". Read and handle each of them now."
}

// reportStatusCheckHint: never guess why a report is missing or late.
const reportStatusCheckHint = "\nIf a report you expected seems missing or late, check the worker's actual status (the workers list / registry) before explaining it; never attribute it to network problems or limits without evidence."
