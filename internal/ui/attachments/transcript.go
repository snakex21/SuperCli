package attachments

import "strings"

// Persisted user messages end with one line naming the attached files, so the
// model still knows which files a later turn refers to. The GUI writes
// "📎 a.png, b.pdf" and the TUI writes "[files] a.png, b.pdf". Interfaces show
// attachments as tiles instead, so they strip that line before display.
var transcriptNameMarkers = []string{"\U0001F4CE ", "[files] "}

// SplitTranscriptNames removes the trailing attachment-name line from a
// persisted user message and returns the remaining text and the names. Text
// without such a line is returned unchanged with nil names. Only a final
// single-line paragraph is recognised, so a marker inside the user's own text
// is never removed.
func SplitTranscriptNames(text string) (string, []string) {
	cut := strings.LastIndex(text, "\n\n")
	start := 0
	if cut >= 0 {
		start = cut + 2
	}
	last := text[start:]
	if strings.Contains(last, "\n") {
		return text, nil
	}
	for _, marker := range transcriptNameMarkers {
		if !strings.HasPrefix(last, marker) {
			continue
		}
		var names []string
		for _, name := range strings.Split(last[len(marker):], ", ") {
			if name = strings.TrimSpace(name); name != "" {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return text, nil
		}
		if cut < 0 {
			return "", names
		}
		return strings.TrimRight(text[:cut], " \t\r\n"), names
	}
	return text, nil
}

// StripTranscriptNames returns the user text without the attachment-name line.
func StripTranscriptNames(text string) string {
	text, _ = SplitTranscriptNames(text)
	return text
}
