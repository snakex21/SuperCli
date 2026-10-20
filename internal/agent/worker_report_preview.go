package agent

import (
	"fmt"
	"strings"

	"supercli/internal/tools/core"
)

// workerReportCompleteModelTextFromSnapshot only encodes a complete inline
// report when it actually saves bytes. The framed encoding is verified against
// the original before use, and lets persistence keep the untouched UI report
// while the model projection retains the smaller representation on resume.
func workerReportCompleteModelTextFromSnapshot(s Snapshot, summary, report, suffix string) string {
	if s.Status != "done" && s.Status != "completed" {
		return ""
	}
	full := renderWorkerNotificationFromSnapshot(s, summary, report) + suffix
	if len(full) > core.ModelOutputInlineBytes || strings.Contains(report, workerReportEncodingPrefix) || strings.Contains(report, workerReportCountedLabel) {
		return ""
	}
	wrapper := renderWorkerNotificationFromSnapshot(s, summary, "")
	compact := workerReportCountRepetitions(report, core.ModelOutputInlineBytes-len(wrapper)-len(suffix)-len(workerReportCountedLabel))
	if compact == "" {
		return ""
	}
	candidate := renderWorkerNotificationFromSnapshot(s, summary, workerReportCountedLabel+compact) + suffix
	marker := fmt.Sprintf("%s%d:%d</report-encoding>\n", workerReportEncodingPrefix, len(compact), len(report))
	candidate = strings.Replace(candidate, "<result>", marker+"<result>", 1)
	if len(candidate) >= len(full) {
		return ""
	}
	if expanded, ok := expandWorkerReportModelText(candidate); !ok || expanded != full {
		return "" // Fail open to the original complete report, never an excerpt.
	}
	return candidate
}

// Keep a map of long structured reports in the existing preview budget. The
// full report and observations remain in Text/RetainedText for UI/read_output.
func workerReportPreview(w *Worker, report, suffix string) string {
	s := w.Snapshot()
	return workerReportPreviewFromSnapshot(s, workerSummaryFromSnapshot(s), report, suffix)
}

func workerReportPreviewFromSnapshot(s Snapshot, summary, report, suffix string) string {
	wrapper := renderWorkerNotificationFromSnapshot(s, summary, "")
	if len(wrapper)+len(report)+len(suffix) <= core.ModelOutputInlineBytes {
		return ""
	}
	// Repeated observations sometimes dominate a worker's prose report. Prefer
	// the complete report with explicit repetition counts over an excerpt, but
	// only when it fits the existing budget. Text/RetainedText remain verbatim.
	const repetitionLabel = "Complete report (adjacent identical lines counted):\n"
	if compact := workerReportCountRepetitions(report, core.ModelOutputPreviewBytes-len(wrapper)-len(suffix)-len(repetitionLabel)); compact != "" {
		return renderWorkerNotificationFromSnapshot(s, summary, repetitionLabel+compact) + suffix
	}
	const outlineLabel = "Report headings (verbatim; omissions marked):\n"
	const excerptLabel = "\nReport excerpt:\n"
	budget := core.ModelOutputPreviewBytes - len(wrapper) - len(suffix) - len(outlineLabel) - len(excerptLabel)
	if budget < 1024 {
		return "" // Preserve the generic fallback for unusually large metadata.
	}
	outline, count := workerReportHeadings(report)
	if count < 3 {
		return ""
	}
	outline = workerReportClip(outline, budget/2)
	excerpt := workerReportClip(report, budget-len(outline))
	preview := renderWorkerNotificationFromSnapshot(s, summary, outlineLabel+outline+excerptLabel+excerpt) + suffix
	if len(preview) > core.ModelOutputPreviewBytes {
		return ""
	}
	return preview
}

// Count only byte-identical adjacent nonblank lines. No unique line is dropped,
// reordered or interpreted as success, instructions or worker-owned metadata.
// Give up rather than excerpting if the complete representation cannot fit.
func workerReportCountRepetitions(report string, budget int) string {
	// Literal marker-shaped prose is not an encoding. Keep the old preview
	// rather than making generated counts indistinguishable from report text.
	if budget <= 0 || strings.Contains(report, "[previous line repeated ") {
		return ""
	}
	var b strings.Builder
	changed := false
	for report != "" {
		end := strings.IndexByte(report, '\n') + 1
		if end == 0 {
			end = len(report)
		}
		line := report[:end]
		report = report[end:]
		repeats := 0
		if strings.TrimSpace(line) != "" {
			for strings.HasPrefix(report, line) {
				report = report[len(line):]
				repeats++
			}
		}
		if len(line) > budget-b.Len() {
			return ""
		}
		b.WriteString(line)
		if repeats > 0 {
			changed = true
			fmt.Fprintf(&b, "[previous line repeated %d additional times]\n", repeats)
		}
		if b.Len() > budget {
			return ""
		}
	}
	if !changed {
		return ""
	}
	return b.String()
}

func workerReportClip(text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	// HeadTail adds a byte/line omission marker. Reserve room for it rather
	// than relying on the output store to truncate the structured preview.
	budget -= 96
	return core.HeadTail(text, budget*2/3, budget-budget*2/3)
}

// Scan ATX headings without allocating one string per report line. The outline
// itself is bounded, including for reports with thousands of headings. Fenced
// examples and indented code must not masquerade as report sections.
func workerReportHeadings(report string) (string, int) {
	outline := core.NewHeadTailBuffer(core.ModelOutputPreviewBytes, core.ModelOutputPreviewBytes)
	count := 0
	var fence byte
	fenceSize := 0
	for report != "" {
		line, rest, _ := strings.Cut(report, "\n")
		report = rest
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimLeft(line, " ")
		if len(line)-len(trimmed) > 3 || trimmed == "" {
			continue
		}
		if trimmed[0] == 96 || trimmed[0] == '~' {
			n := 1
			for n < len(trimmed) && trimmed[n] == trimmed[0] {
				n++
			}
			if n >= 3 {
				if fence == 0 {
					fence, fenceSize = trimmed[0], n
				} else if trimmed[0] == fence && n >= fenceSize && strings.TrimSpace(trimmed[n:]) == "" {
					fence, fenceSize = 0, 0
				}
				continue
			}
		}
		if fence != 0 {
			continue
		}
		level := 0
		for level < len(trimmed) && trimmed[level] == '#' {
			level++
		}
		if level < 1 || level > 6 || level == len(trimmed) || (trimmed[level] != ' ' && trimmed[level] != '\t') {
			continue
		}
		if strings.TrimSpace(trimmed[level:]) == "" {
			continue
		}
		_, _ = outline.Write([]byte(line))
		_, _ = outline.Write([]byte("\n"))
		count++
	}
	return outline.String(), count
}
