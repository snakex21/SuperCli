package agent

import (
	"strings"
	"unicode/utf8"

	"supercli/internal/llm"
	"supercli/internal/tools/core"
)

const workerReportEncodingPrefix = "<report-encoding>adjacent-lines-v1:"
const workerReportCountedLabel = "Complete report (adjacent identical lines counted):\n"

// expandWorkerReportModelText accepts only the host's bounded, explicitly framed
// inline encoding. In particular, old unmarked previews and marker-shaped prose
// inside a raw report are never interpreted. Every failure returns the input.
func expandWorkerReportModelText(text string) (string, bool) {
	fail := func() (string, bool) { return text, false }
	if len(text) > core.ModelOutputInlineBytes || !utf8.ValidString(text) || !strings.HasPrefix(text, "<task-notification>\n") {
		return fail()
	}
	result := strings.Index(text, "<result>")
	if result < 0 {
		return fail()
	}
	marker := strings.Index(text[:result], "<report-encoding>")
	if marker < 0 || marker == 0 || text[marker-1] != '\n' || !strings.HasPrefix(text[marker:result], workerReportEncodingPrefix) {
		return fail()
	}
	fields, ok := strings.CutSuffix(text[marker+len(workerReportEncodingPrefix):result], "</report-encoding>\n")
	if !ok {
		return fail()
	}
	compactField, expandedField, ok := strings.Cut(fields, ":")
	if !ok {
		return fail()
	}
	compactLen, ok := workerReportBoundedPositive(compactField, core.ModelOutputInlineBytes)
	if !ok {
		return fail()
	}
	expandedLen, ok := workerReportBoundedPositive(expandedField, core.ModelOutputInlineBytes)
	if !ok {
		return fail()
	}
	start := result + len("<result>")
	if !strings.HasPrefix(text[start:], workerReportCountedLabel) {
		return fail()
	}
	start += len(workerReportCountedLabel)
	if compactLen > len(text)-start {
		return fail()
	}
	end := start + compactLen
	if !strings.HasPrefix(text[end:], "</result>\n</task-notification>") || !utf8.ValidString(text[start:end]) {
		return fail()
	}
	// Bound the entire restored notification, including metadata and suffix,
	// before allocating or multiplying any repeated line.
	if marker+len("<result>")+expandedLen+len(text)-end > core.ModelOutputInlineBytes {
		return fail()
	}
	var decoded strings.Builder
	decoded.Grow(expandedLen)
	payload := text[start:end]
	previous := ""
	counted := false
	for payload != "" {
		n := strings.IndexByte(payload, '\n') + 1
		if n == 0 {
			n = len(payload)
		}
		line := payload[:n]
		payload = payload[n:]
		if strings.Contains(line, "[previous line repeated ") {
			const prefix = "[previous line repeated "
			const suffix = " additional times]\n"
			if len(line) <= len(prefix)+len(suffix) || !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) || previous == "" || !strings.HasSuffix(previous, "\n") || strings.TrimSpace(previous) == "" {
				return fail()
			}
			count, valid := workerReportBoundedPositive(line[len(prefix):len(line)-len(suffix)], core.ModelOutputInlineBytes)
			if !valid || count > (expandedLen-decoded.Len())/len(previous) {
				return fail()
			}
			for range count {
				decoded.WriteString(previous)
			}
			previous = "" // A count must immediately follow an ordinary line.
			counted = true
			continue
		}
		if len(line) > expandedLen-decoded.Len() {
			return fail()
		}
		decoded.WriteString(line)
		previous = line
	}
	if !counted || decoded.Len() != expandedLen {
		return fail()
	}
	return text[:marker] + "<result>" + decoded.String() + text[end:], true
}

// Parse canonical positive decimal without conversion overflow or large counts.
func workerReportBoundedPositive(s string, limit int) (int, bool) {
	if s == "" || s[0] == '0' {
		return 0, false
	}
	n := 0
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		d := int(s[i] - '0')
		if n > (limit-d)/10 {
			return 0, false
		}
		n = n*10 + d
	}
	return n, n > 0 && n <= limit
}

// canonicalWorkerReportMessage restores only host-owned plain-text reports for
// new transcript appends. The caller's model message and projection stay compact.
func canonicalWorkerReportMessage(msg llm.Message) llm.Message {
	if len(msg.Parts) != 0 || len(msg.ToolCalls) != 0 {
		return msg
	}
	if !(msg.Role == llm.RoleTool && (msg.Name == "task" || msg.Name == "send_message") || msg.Role == llm.RoleUser && msg.Name == llm.WorkerNotificationName) {
		return msg
	}
	if expanded, ok := expandWorkerReportModelText(msg.Content); ok {
		msg.Content = expanded
	}
	return msg
}
