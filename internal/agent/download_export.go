package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"supercli/internal/llm"
	"supercli/internal/tools/sandbox"
)

var downloadsDestination = regexp.MustCompile(`(?:^|\s)(?:do|w|to|in|into)\s+(?:(?:moich|mojego|my|the|folderu|folderze|folder|katalogu)\s+)*(?:pobranych|pobrane|downloads)(?:\s|[,!?;.]|$)`)
var downloadDestinationMention = regexp.MustCompile(`(?:^|\s)(?:do|w|to|in|into)\s+\S+`)
var absoluteDownloadPath = regexp.MustCompile("(?m)(?:^|[\\s(\\[])([A-Za-z]:[\\\\/][^\\s<>\"|?*`;!]+|\\\\\\\\[^\\s<>\"|?*`;!]+|/[^\\s<>\"|?*`;!]+)")

type downloadRequestContext struct {
	download, publicWeb, neutral, destinationSpecified bool
	downloads                                          bool
	directories                                        []string
	files                                              []string
}

// Raw transcript access is distinct from the provider projection: summaries,
// repository addons and tool results must never become export authorization.
type userRequestHistoryReader interface {
	ReadUserRequestHistory(context.Context, int) ([]llm.Message, error)
}

func requestsDownloadsExport(prompt string) bool {
	if !containsFileDownloadReference(prompt) {
		return false
	}
	p := unquotedActionPrompt(strings.ToLower(prompt))
	match := downloadsDestination.FindStringIndex(p)
	if match == nil || webActionExampleContext(p[:match[0]]) || !isWebActionDirective(p[:match[0]]) || !downloadOutputPathContext(p[:match[1]], p[match[1]:]) {
		return false
	}
	before := p[:match[0]]
	clause := before[strings.LastIndexAny(before, ".!?;\n")+1:]
	for _, word := range strings.Fields(clause) {
		word = strings.Trim(word, ",.!?()")
		switch word {
		case "nie", "not", "never", "don't", "don’t", "bez", "except", "przykład", "przyklad", "example":
			return false
		}
	}
	return true
}

// Directory spellings come only from the real user's text. Quoted paths are
// path data; quoted commands/examples cannot supply the download action.
func requestedDownloadDirectories(prompt string) []string {
	if !containsFileDownloadReference(prompt) {
		return nil
	}
	return downloadDirectoriesFromPrompt(prompt)
}

func downloadDirectoriesFromPrompt(prompt string) []string {
	directories, _ := downloadTargetsFromPrompt(prompt)
	return directories
}

func downloadTargetsFromPrompt(prompt string) ([]string, []string) {
	var directories, files []string
	add := func(path, before, after string) {
		if len(directories)+len(files) >= 64 {
			return
		}
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) || webActionExampleContext(strings.ToLower(before)) || !isWebActionDirective(strings.ToLower(before)) || !downloadOutputPathContext(before, after) {
			return
		}
		exactFile := isExactDownloadFileTarget(path, before)
		path = filepath.Clean(path)
		for _, paths := range [][]string{directories, files} {
			for _, existing := range paths {
				if strings.EqualFold(existing, path) {
					return
				}
			}
		}
		if exactFile {
			files = append(files, path)
		} else {
			directories = append(directories, path)
		}
	}
	// Preserve quoted directory paths (including spaces), then mask every
	// quotation before scanning plain absolute paths.
	runes := []rune(prompt)
	for start := 0; start < len(runes); start++ {
		closing := runes[start]
		switch closing {
		case '"', '`', '\'', '“', '„', '«', '‘':
			if (closing == '\'' || closing == '‘') && start > 0 && unicode.IsLetter(runes[start-1]) {
				continue // Apostrophes inside words are not path delimiters.
			}
			if closing == '“' || closing == '„' {
				closing = '”'
			} else if closing == '«' {
				closing = '»'
			} else if closing == '‘' {
				closing = '’'
			}
		default:
			continue
		}
		end := start + 1
		for end < len(runes) && runes[end] != closing {
			end++
		}
		if end == len(runes) {
			break
		}
		add(string(runes[start+1:end]), string(runes[:start]), string(runes[end+1:]))
		start = end
	}
	p := unquotedActionPrompt(prompt)
	for _, match := range absoluteDownloadPath.FindAllStringSubmatchIndex(p, -1) {
		add(strings.TrimRight(p[match[2]:match[3]], ",."), p[:match[2]], p[match[3]:])
	}
	return directories, files
}

func isExactDownloadFileTarget(path, before string) bool {
	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\") || !isDownloadAssetExtension(strings.ToLower(filepath.Ext(path))) {
		return false
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return false
	}
	// A named folder may contain a dot or an asset suffix. The closest
	// direction cue starts a new target, so an earlier source-folder label
	// cannot change a later explicit filename into a directory grant.
	clause := strings.ToLower(unquotedActionPrompt(before))
	clause = clause[strings.LastIndexAny(clause, ".!?;\n")+1:]
	folder := false
	for _, word := range strings.FieldsFunc(clause, func(r rune) bool { return !unicode.IsLetter(r) }) {
		switch word {
		case "to", "into", "do", "in", "w", "as", "jako", "here", "there", "tutaj", "tu", "tam":
			folder = false
		case "folder", "folderu", "folderze", "katalog", "katalogu", "directory", "dir":
			folder = true
		}
	}
	return !folder
}

// The presence of a download verb does not authorize every path mentioned in
// the request. A destination cue can introduce a path; sources, exclusions and
// factual mentions cannot. A bare path (or folder label) is a direct choice in
// the already-authorized download continuation handled by the caller.
func downloadOutputPathContext(before, after string) bool {
	before = strings.ToLower(strings.TrimRight(before, " \t\"`'(["))
	before = absoluteDownloadPath.ReplaceAllString(unquotedActionPrompt(before), " ")
	before = strings.TrimSpace(before)
	if before == "" {
		return downloadPathChoiceEnds(after)
	}
	// A natural destination choice may finish with a question: "here? C:\\...".
	// Inspect that preceding phrase rather than treating an empty clause as a
	// blanket grant for an unrelated path after any punctuation.
	before = strings.TrimRight(before, ".!?;\n \t")
	clause := before[strings.LastIndexAny(before, ".!?;\n")+1:]
	words := strings.FieldsFunc(clause, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) == 0 {
		return false
	}
	source, excluded, destination := false, false, false
	label := true
	for _, word := range words {
		switch word {
		case "folder", "folderu", "folderze", "katalog", "katalogu", "directory", "dir", "path", "ścieżka", "sciezka":
			// Labels alone are useful for a directory-only answer. These nouns
			// do not turn "from folder ..." into output authorization.
		case "from", "z", "read", "open", "otwórz", "otworz", "czytaj":
			source, destination, label = true, false, false
		case "instead", "zamiast", "except", "excluding", "exclude", "oprócz", "oprocz":
			excluded, destination, label = true, false, false
		case "to", "into", "do", "as", "jako", "here", "there", "tutaj", "tu", "tam", "destination", "output", "docelowy", "docelowego":
			if !excluded {
				source, destination = false, true
			}
		case "in", "w", "on", "na":
			if !source && !excluded {
				destination = true
			}
		case "download", "pobierz", "pobrać", "pobrac", "ściągnij", "sciagnij", "save", "zapisz", "put", "daj", "dać", "dac":
			// An independent positive instruction can follow "instead".
			source, excluded, destination, label = false, false, false, false
		default:
			label = false
		}
	}
	return !source && !excluded && (destination || label && downloadPathChoiceEnds(after))
}

func downloadPathChoiceEnds(after string) bool {
	return strings.Trim(after, " \t\r\n,.;!?\"'`)]}") == ""
}

func promptWithoutDownloadPaths(prompt string) string {
	return absoluteDownloadPath.ReplaceAllString(prompt, " ")
}

func parseDownloadRequest(prompt string) downloadRequestContext {
	request := downloadRequestContext{download: containsFileDownloadReference(prompt)}
	if !request.download {
		mode, confident := DefaultRouteMap().ClassifyConfident(prompt)
		request.neutral = confident && mode == RouteChatOnly
		return request
	}
	request.publicWeb = isSelfContainedWebRequest(prompt)
	request.downloads = requestsDownloadsExport(prompt)
	request.directories, request.files = downloadTargetsFromPrompt(prompt)
	request.destinationSpecified = request.downloads || len(request.directories)+len(request.files) > 0 || downloadDestinationMention.MatchString(unquotedActionPrompt(strings.ToLower(prompt)))
	return request
}

func (l *Loop) downloadRequestForPrompt(prompt string) downloadRequestContext {
	request := parseDownloadRequest(prompt)
	if request.download {
		return request
	}
	p := unquotedActionPrompt(strings.ToLower(prompt))
	// An output-directory choice continues an active download task without
	// requiring the human to repeat its verb, source or opening phrase.
	if !l.previousDownloadIsActive() || actionMentionContext(p) || webActionExampleContext(p) || !isWebActionDirective(p) || hasLocalWebRequestWork(p, true) {
		return request
	}
	directories, files := downloadTargetsFromPrompt(prompt)
	if len(directories)+len(files) == 0 && !downloadsDestination.MatchString(p) {
		return request
	}
	request.download = true
	request.neutral = false
	request.directories = directories
	request.files = files
	request.downloads = downloadsDestination.MatchString(p)
	request.destinationSpecified = true
	return request
}

func (l *Loop) previousDownloadIsActive() bool {
	for i := len(l.downloadHumanContext) - 1; i >= 0; i-- {
		if !l.downloadHumanContext[i].neutral {
			return l.downloadHumanContext[i].download
		}
	}
	return false
}

func (l *Loop) rememberUserDownloadRequest(prompt string) {
	l.downloadHumanContext = append(l.downloadHumanContext, l.downloadRequestForPrompt(prompt))
	if len(l.downloadHumanContext) > 64 {
		copy(l.downloadHumanContext, l.downloadHumanContext[len(l.downloadHumanContext)-64:])
		l.downloadHumanContext = l.downloadHumanContext[:64]
	}
}

func (l *Loop) restoreUserDownloadHistory(raw []llm.Message) {
	l.downloadHumanContext = nil
	for _, message := range raw {
		if !isConversationUserTurn(message) {
			continue
		}
		text := message.TextOnly().Content
		if strings.HasPrefix(strings.TrimSpace(text), compactSummaryPreamble) || strings.HasPrefix(strings.TrimSpace(text), legacyCompactSummaryPreamble) {
			continue
		}
		l.rememberUserDownloadRequest(text)
	}
	l.downloadHistoryLoaded = true
}

func (l *Loop) loadUserDownloadHistory(ctx context.Context) error {
	if l.userDownloadsDir == nil {
		return nil
	}
	sessionID := l.SessionID()
	if l.downloadHistoryLoaded && sessionID == l.downloadHistorySession {
		return nil
	}
	l.downloadHumanContext = nil
	l.downloadHistorySession = sessionID
	// A failed read fails closed for inherited grants; the new raw human
	// instruction remains usable and never depends on provider history.
	if reader, ok := l.writer.(userRequestHistoryReader); ok {
		raw, err := reader.ReadUserRequestHistory(ctx, 64)
		if err != nil {
			return err
		}
		l.restoreUserDownloadHistory(raw)
	}
	l.downloadHistoryLoaded = true
	return nil
}

func (l *Loop) previousDownloadDestination() downloadRequestContext {
	for i := len(l.downloadHumanContext) - 1; i >= 0; i-- {
		request := l.downloadHumanContext[i]
		if request.neutral {
			continue
		}
		if !request.download {
			break
		}
		if request.destinationSpecified {
			return request
		}
	}
	return downloadRequestContext{}
}

func (l *Loop) previousDownloadWasWeb() bool {
	for i := len(l.downloadHumanContext) - 1; i >= 0; i-- {
		request := l.downloadHumanContext[i]
		if request.neutral {
			continue
		}
		if !request.download {
			return false
		}
		if request.publicWeb {
			return true
		}
	}
	return false
}

func (l *Loop) prepareUserDownloadExport(ctx context.Context, prompt string) (context.Context, string) {
	if l.userDownloadsDir == nil {
		// Generated worker text cannot extend the parent grant.
		return ctx, ""
	}
	historyErr := l.loadUserDownloadHistory(ctx)
	ctx = sandbox.WithDownloadExportDirs(ctx)
	request := l.downloadRequestForPrompt(prompt)
	if !request.download {
		return ctx, ""
	}
	if !request.destinationSpecified {
		if historyErr != nil {
			return ctx, "The previous raw user download request could not be read. Do not infer an export directory from model summaries or tool output; use a destination explicitly supplied by the current user."
		}
		prior := l.previousDownloadDestination()
		request.downloads, request.directories, request.files = prior.downloads, prior.directories, prior.files
	}
	directories := append([]string(nil), request.directories...)
	files := append([]string(nil), request.files...)
	if request.downloads {
		dir, err := l.userDownloadsDir()
		if err != nil || !filepath.IsAbs(dir) {
			return ctx, "The requested Downloads folder could not be resolved; do not guess an absolute user folder path."
		}
		directories = append(directories, dir)
	}
	if len(directories)+len(files) == 0 {
		return ctx, ""
	}
	granted := sandbox.WithDownloadExportTargets(ctx, directories, files)
	for _, dir := range directories {
		if _, err := sandbox.ResolveDownloadDestination(granted, l.baseDir, filepath.Join(dir, ".supercli-export-check")); err != nil {
			return ctx, "The requested download folder could not be authorized; do not substitute a different folder."
		}
	}
	for _, file := range files {
		if _, err := sandbox.ResolveDownloadDestination(granted, l.baseDir, file); err != nil {
			return ctx, "The requested download filename could not be authorized; do not substitute a parent folder or a different filename."
		}
	}
	display := make([]string, len(directories))
	for i, dir := range directories {
		display[i] = filepath.ToSlash(dir)
	}
	fileDisplay := make([]string, len(files))
	for i, file := range files {
		fileDisplay[i] = filepath.ToSlash(file)
	}
	return granted, fmt.Sprintf("The user requested download output in folders %q and at these exact filenames %q. Use web_download with an absolute path for each new file; exact filename grants allow no sibling or child paths. It refuses to overwrite existing files. A successful result verifies file size and SHA256. Outputs outside the project are not covered by project Undo/Redo.", display, fileDisplay)
}
