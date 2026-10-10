package agent

import (
	"net/url"
	"path"
	"strings"
	"unicode"
)

// An explicit file download warrants the exact contract even when the model
// first needs to find the URL. This only advertises a tool; it never fetches a
// URL, selects a destination or grants access outside the workspace.
func containsFileDownloadReference(prompt string) bool {
	return hasExplicitWebAction(prompt, "download", "pobierz", "pobrać", "pobrac", "sciagnij", "ściągnij", "ściągnąć", "sciagnac", "web_download")
}

// Work with unquoted instructions rather than URL/file names, quoted examples
// or descriptions of a command. False negatives only retain normal discovery.
func hasExplicitWebAction(prompt string, actions ...string) bool {
	p := unquotedActionPrompt(strings.ToLower(prompt))
	offset := 0
	for _, part := range strings.Fields(p) {
		at := offset + strings.Index(p[offset:], part)
		offset = at + len(part)
		// Download/page names inside a URL are not a request to fetch that URL.
		if strings.Contains(part, "http://") || strings.Contains(part, "https://") {
			continue
		}
		for _, word := range strings.FieldsFunc(part, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }) {
			for _, action := range actions {
				if word != action {
					continue
				}
				before := p[:at+strings.Index(part, word)]
				if !actionMentionContext(before) && !webActionExampleContext(before) && isWebActionDirective(before) {
					return true
				}
			}
		}
	}
	return false
}

func isWebActionDirective(before string) bool {
	clause := before[strings.LastIndexAny(before, ".!?;\n")+1:]
	clause = strings.Join(strings.FieldsFunc(clause, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(",:()", r)
	}), " ")
	clause = strings.TrimSpace(clause)
	// Negation also governs coordinated verbs: "do not download or find" is
	// not a request to perform the second action. A new sentence/semicolon can
	// still introduce an explicit positive directive.
	for _, word := range strings.Fields(clause) {
		switch word {
		case "not", "never", "don't", "don’t", "dont", "cannot", "can't", "can’t", "nie", "bez":
			return false
		case "model", "assistant", "asystent", "tried", "próbował", "probowal", "word", "słowo", "slowo", "command", "polecenie", "komunikat", "log", "dokumencie", "napisano", "document", "says":
			// Reported/mentioned actions are not a directive. The opening of
			// a real request otherwise has no fixed greeting or phrase grammar.
			return false
		}
	}
	return true
}

func webActionExampleContext(before string) bool {
	// A label on the preceding line still introduces an example, rather than
	// a new user instruction ("Example:\n download ...").
	before = strings.TrimSpace(before)
	clause := before[strings.LastIndexAny(before, ".!?;\n")+1:]
	for _, marker := range []string{"example", "przykład", "przyklad", "the command", "polecenie", "komunikat", "the log", "w logu"} {
		if strings.Contains(clause, marker) {
			return true
		}
	}
	return false
}

// A public-web request does not need repository collection or a navigator
// round trip. Keep this narrow: local search, mixed project/command requests,
// explanations and ambiguous continuations keep their normal routing.
func isSelfContainedWebRequest(prompt string) bool {
	p := unquotedActionPrompt(strings.ToLower(prompt))
	download := containsFileDownloadReference(p)
	if !download && !hasExplicitWebAction(p, "find", "search", "lookup", "znajdź", "znajdz", "wyszukaj", "poszukaj") {
		return false
	}
	if !hasPublicWebSource(p) {
		return false
	}
	return !hasLocalWebRequestWork(p, download)
}

// Source recognition follows the requested medium and source relation, not a
// provider's name. It only avoids unrelated repository preparation; it neither
// selects a tool/URL nor grants filesystem access. Bare resource requests remain
// ambiguous, and the separate local/mixed-work guard still takes precedence.
func hasPublicWebSource(prompt string) bool {
	if strings.Contains(prompt, "http://") || strings.Contains(prompt, "https://") {
		return true
	}
	words := strings.FieldsFunc(prompt, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	asset, source := false, false
	for index, word := range words {
		if word == "web" || word == "online" || strings.HasPrefix(word, "interne") || strings.HasPrefix(word, "website") || strings.HasPrefix(word, "site") || strings.HasPrefix(word, "stron") || strings.HasPrefix(word, "witryn") {
			return true
		}
		for _, medium := range []string{"gif", "image", "photo", "picture", "obraz", "zdję", "zdje", "audio", "video", "wideo", "sound", "dźwię", "dzwie", "pdf", "resource", "zasob", "zasób", "asset"} {
			asset = asset || strings.HasPrefix(word, medium)
		}
		if index+1 < len(words) {
			switch word {
			case "from", "on", "at", "z", "ze", "na":
				source = true
			}
		}
	}
	return asset && source
}

// Output paths do not turn a public download into repository work. All other
// local actions remain visible, including clauses after URLs and paths.
func hasLocalWebRequestWork(prompt string, download bool) bool {
	p := promptWithoutDownloadPaths(prompt)
	var local strings.Builder
	for _, part := range strings.Fields(p) {
		if !strings.Contains(part, "http://") && !strings.Contains(part, "https://") {
			local.WriteString(part)
			local.WriteByte(' ')
		}
	}
	for _, word := range strings.FieldsFunc(local.String(), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' }) {
		if !download {
			for _, marker := range []string{"plik", "file"} {
				if strings.HasPrefix(word, marker) {
					return true
				}
			}
		}
		for _, marker := range []string{
			"repo", "projekt", "project", "kod", "code", "funkcj", "function", "test", "build",
			"folder", "director", "local", "lokal", "dysk", "disk",
			"napraw", "fix", "zmien", "zmień", "edyt", "edit", "implement", "zaimplement",
			"uruchom", "run", "terminal", "shell", "powershell", "cmd", "komend", "command",
			"skrypt", "script", "pakiet", "package", "npm", "python", "node",
			"usuń", "usun", "delete", "remove", "przenieś", "przenies", "move", "rename", "install", "zainstal",
		} {
			if strings.HasPrefix(word, marker) {
				return true
			}
		}
	}
	return false
}

// A known file URL needs only download. Pages, extensionless endpoints and
// URLs still to be found expose the reader too, regardless of the provider.
// Availability does not force a fetch or choose a URL for the model.
func needsDownloadPageContract(prompt string) bool {
	foundURL := false
	for _, token := range strings.Fields(prompt) {
		lower := strings.ToLower(token)
		at := strings.Index(lower, "https://")
		if at < 0 {
			at = strings.Index(lower, "http://")
		}
		if at < 0 {
			continue
		}
		raw := strings.TrimRight(token[at:], "\"'`)],.;!?")
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		foundURL = true
		if !isDownloadAssetExtension(strings.ToLower(path.Ext(parsed.Path))) {
			return true
		}
	}
	return !foundURL
}

func isDownloadAssetExtension(extension string) bool {
	switch extension {
	case ".gif", ".png", ".jpg", ".jpeg", ".webp", ".avif", ".bmp", ".ico", ".svg", ".tif", ".tiff",
		".mp4", ".webm", ".mov", ".m4v", ".mkv", ".avi", ".mp3", ".wav", ".ogg", ".flac", ".aac", ".m4a", ".opus",
		".woff", ".woff2", ".ttf", ".otf", ".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar",
		".pdf", ".docx", ".xlsx", ".pptx", ".csv", ".txt", ".json", ".xml", ".yaml", ".yml", ".wasm", ".bin", ".exe", ".msi", ".dmg":
		return true
	}
	return false
}
