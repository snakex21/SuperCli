package files

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"supercli/internal/tools/core"
)

func renderReadMany(outcomes []readManyOutcome) Result {
	perItem := min(maxReadManyBytes/len(outcomes), maxReadManyItem)
	var display, retained strings.Builder
	okCount, failedCount, largestBody := 0, 0, 0
	for i, outcome := range outcomes {
		header := fmt.Sprintf("== [%d] %s:%d-%d ==\n", i+1, outcome.request.File, outcome.request.From, outcome.request.To)
		display.WriteString(header)
		retained.WriteString(header)
		body := outcome.text
		if outcome.err != nil {
			failedCount++
			body = fmt.Sprintf("error: %v\n", outcome.err)
			display.WriteString(body)
		} else {
			okCount++
			largestBody = max(largestBody, len(body))
			display.WriteString(core.HeadTail(body, perItem*3/4, perItem-perItem*3/4))
		}
		retained.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			display.WriteByte('\n')
			retained.WriteByte('\n')
		}
	}
	summary := fmt.Sprintf("[read_many: %d ok, %d failed]", okCount, failedCount)
	display.WriteString(summary)
	retained.WriteString(summary)
	// Equal per-item shares can truncate one file while tiny siblings leave
	// most of the budget unused. Keep the complete captured batch when it
	// fits the existing inline and per-item bounds; failed batches stay below.
	if len(outcomes) > 1 && failedCount == 0 && largestBody <= maxReadManyItem && retained.Len() <= core.ModelReadBatchInlineBytes {
		return Result{Text: retained.String()}
	}
	result := Result{Text: core.HeadTail(display.String(), maxReadManyBytes*3/4, maxReadManyBytes/4)}
	// Retain the bounded, numbered ranges BEFORE per-item/UI compaction.
	// This lets read_output inspect omitted evidence without rereading files.
	if retained.String() != result.Text {
		result.RetainedText = retained.String()
	}
	inlineLimit := core.ModelOutputInlineBytes
	// Preserve a complete moderate batch; expanding an already partial or
	// single-file result would spend more context without removing a read.
	if len(outcomes) > 1 && failedCount == 0 && result.RetainedText == "" {
		inlineLimit = core.ModelReadBatchInlineBytes
	}
	if len(result.Text) > inlineLimit {
		result.ModelPreview = readManyPreview(outcomes, summary)
	}
	return result
}

// Reserve each header first, then divide the existing model preview budget
// among file bodies. Small sections release their unused share to larger ones.
// A generic head/tail preview would erase middle files and their errors.
func readManyPreview(outcomes []readManyOutcome, summary string) string {
	headers := make([]string, len(outcomes))
	bodies := make([]string, len(outcomes))
	order := make([]int, len(outcomes))
	remaining := core.ModelOutputPreviewBytes - len(summary)
	for i, outcome := range outcomes {
		headers[i] = fmt.Sprintf("== [%d] %s:%d-%d ==\n", i+1, previewFileName(outcome.request.File), outcome.request.From, outcome.request.To)
		bodies[i] = outcome.text
		if outcome.err != nil {
			bodies[i] = fmt.Sprintf("error: %v", outcome.err)
		}
		remaining -= len(headers[i]) + 1 // newline after each body
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return len(bodies[order[i]]) < len(bodies[order[j]]) })
	for position, i := range order {
		share := remaining / (len(order) - position)
		if len(bodies[i]) > share {
			// HeadTail adds an omission marker; reserve its maximum size.
			keep := max(0, share-96)
			bodies[i] = core.HeadTail(bodies[i], keep*3/4, keep-keep*3/4)
		}
		remaining -= len(bodies[i])
	}
	var b strings.Builder
	for i := range outcomes {
		b.WriteString(headers[i])
		b.WriteString(bodies[i])
		b.WriteByte('\n')
	}
	b.WriteString(summary)
	return b.String()
}

func previewFileName(path string) string {
	const limit = 96
	if len(path) <= limit {
		return path
	}
	head, tail := 32, len(path)-(limit-32-3)
	for head > 0 && !utf8.RuneStart(path[head]) {
		head--
	}
	for tail < len(path) && !utf8.RuneStart(path[tail]) {
		tail++
	}
	return path[:head] + "..." + path[tail:]
}
