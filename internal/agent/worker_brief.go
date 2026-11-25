package agent

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"supercli/internal/llm"
)

// Worker brief (after 1.0.40). A worker whose first request failed (a usage
// limit, a model-not-found error) lost its brief: the failed turn's user
// message was rolled back, every later resume only appended "continue from
// where you stopped", and the resumed worker had no idea what its task was
// (the coordinator had to resend five briefs and guessed which worker did
// what). The brief is now pinned: every request of the worker carries it as
// a leading system block, request-only, so no rollback, compaction, elision,
// restart or model change can remove it. Reports and notices name each
// worker's task so the coordinator never has to guess the mapping.

// workerPinnedBriefPrefix opens the pinned block.
const workerPinnedBriefPrefix = "[Your task from the coordinator - kept for every run of this worker; later messages may refine it]\n"

// maxPinnedBriefBytes bounds the pinned copy of a very long brief.
const maxPinnedBriefBytes = 24_000

func pinnedWorkerBrief(brief string) string {
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return ""
	}
	if len(brief) > maxPinnedBriefBytes {
		brief = compactHeadTail(brief, maxPinnedBriefBytes)
	}
	return workerPinnedBriefPrefix + brief
}

var taskGoalLine = regexp.MustCompile(`(?i)^(cel|goal|rola|role|zadanie|task|twoje zadanie|your task)\s*:\s*`)

// workerTaskTitle is the worker's task in a few words: a GOAL/TASK/CEL line
// when the brief has one (briefs often open with shared project boilerplate),
// else its first line. The same rule as the GUI's card title.
func workerTaskTitle(brief string) string {
	var first, goal string
	for _, line := range strings.Split(strings.ReplaceAll(brief, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if first == "" {
			first = line
		}
		if taskGoalLine.MatchString(line) {
			goal = taskGoalLine.ReplaceAllString(line, "")
			break
		}
	}
	title := goal
	if title == "" {
		title = first
	}
	title = strings.Join(strings.Fields(title), " ")
	if len(title) > 120 {
		cut := 117
		for cut > 0 && !utf8.RuneStart(title[cut]) {
			cut--
		}
		title = title[:cut] + "..."
	}
	return strings.NewReplacer("<", "(", ">", ")").Replace(title)
}

// withPinnedTask inserts the pinned brief after the leading system messages
// of a provider view (a copy; the loop's history is untouched).
func withPinnedTask(view []llm.Message, pinned string) []llm.Message {
	if pinned == "" {
		return view
	}
	lead := leadingSystemCount(view)
	out := make([]llm.Message, 0, len(view)+1)
	out = append(out, view[:lead]...)
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: pinned, Origin: &llm.ContextOrigin{Source: llm.SourceInstructions}})
	return append(out, view[lead:]...)
}
