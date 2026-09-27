package agent

import (
	"strings"

	"supercli/internal/tools/core"
)

// Keep a map of long structured reports in the existing preview budget. The
// full report and observations remain in Text/RetainedText for UI/read_output.
func workerReportPreview(w *Worker, report, suffix string) string {
	wrapper := renderWorkerNotification(w, "")
	if len(wrapper)+len(report)+len(suffix) <= core.ModelOutputInlineBytes {
		return ""
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
	preview := renderWorkerNotification(w, outlineLabel+outline+excerptLabel+excerpt) + suffix
	if len(preview) > core.ModelOutputPreviewBytes {
		return ""
	}
	return preview
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
