package files

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	core "supercli/internal/tools/core"
	"supercli/internal/tools/fileops"
)

const (
	maxReadManyRequests = 12
	maxReadManyRange    = 300
	maxReadManyBytes    = 32 * 1024
	maxReadManyItem     = 8 * 1024
	maxReadManyLineKeep = 1800
)

// ReadMany reads independent line ranges in one tool call. It is useful on
// both local and cloud models: one provider round-trip replaces a sequence of
// read_lines turns. Reads execute concurrently, but results are rendered in
// request order so the model sees deterministic context.
type ReadMany struct{ BaseDir string }

func NewReadMany(baseDir string) *ReadMany { return &ReadMany{BaseDir: baseDir} }

type readManyRequest struct {
	File string `json:"file"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

type readManyArgs struct {
	Reads json.RawMessage `json:"reads"`
}

func (t *ReadMany) Spec() Tool {
	return Tool{
		Name: "read_many",
		Description: "Read up to 12 independent file ranges in one call to save model turns. " +
			"Use 'file:from-to | file:from-to'; file may contain a glob such as internal/*.go. " +
			"Works with native and thin/sentinel tool calling.",
		ReadOnly: true,
		Schema: `{
  "type": "object",
  "properties": {
    "reads": {"type":"string","description":"Ranges separated by |: file:from-to | file:from-to (max 12, 300 lines each)"}
  },
  "required": ["reads"]
}`,
		Fn: t.execute,
	}
}

func (t *ReadMany) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var raw readManyArgs
	if err := json.Unmarshal(args, &raw); err != nil {
		return Result{Err: fmt.Errorf("read_many: bad args: %w", err)}, nil
	}
	requests, err := decodeReadManyRequests(raw.Reads)
	if err != nil {
		return Result{Err: fmt.Errorf("read_many: %w", err)}, nil
	}
	if len(requests) == 0 {
		return Result{Err: fmt.Errorf("read_many: reads is empty")}, nil
	}
	if len(requests) > maxReadManyRequests {
		return Result{Err: fmt.Errorf("read_many: %d reads exceeds cap %d", len(requests), maxReadManyRequests)}, nil
	}
	work := expandReadManyRequests(t.BaseDir, requests)
	if len(work) > maxReadManyRequests {
		return Result{Err: fmt.Errorf("read_many: glob expansion produced %d reads, exceeds cap %d", len(work), maxReadManyRequests)}, nil
	}

	type outcome struct {
		request readManyRequest
		text    string
		err     error
	}
	outcomes := make([]outcome, len(work))
	var wg sync.WaitGroup
	for i, item := range work {
		i, item := i, item
		request := item.request
		outcomes[i].request = request
		wg.Add(1)
		go func() {
			defer wg.Done()
			if item.err != nil {
				outcomes[i].err = item.err
				return
			}
			if err := ctx.Err(); err != nil {
				outcomes[i].err = err
				return
			}
			if err := validateReadManyRequest(request); err != nil {
				outcomes[i].err = err
				return
			}
			path, err := resolveSandboxed(t.BaseDir, request.File)
			if err != nil {
				outcomes[i].err = err
				return
			}
			lines, err := readManyLinesStreaming(path, request.From, request.To)
			if err != nil {
				outcomes[i].err = err
				return
			}
			outcomes[i].text = renderLines(lines)
		}()
	}
	wg.Wait()

	perItem := maxReadManyBytes / len(outcomes)
	if perItem > maxReadManyItem {
		perItem = maxReadManyItem
	}
	var b strings.Builder
	okCount, failedCount := 0, 0
	for i, outcome := range outcomes {
		fmt.Fprintf(&b, "== [%d] %s:%d-%d ==\n", i+1, outcome.request.File, outcome.request.From, outcome.request.To)
		if outcome.err != nil {
			failedCount++
			fmt.Fprintf(&b, "error: %v\n", outcome.err)
			continue
		}
		okCount++
		head := perItem * 3 / 4
		tail := perItem - head
		b.WriteString(core.HeadTail(outcome.text, head, tail))
		if !strings.HasSuffix(outcome.text, "\n") {
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "[read_many: %d ok, %d failed]", okCount, failedCount)
	return Result{Text: core.HeadTail(b.String(), maxReadManyBytes*3/4, maxReadManyBytes/4)}, nil
}

type readManyWork struct {
	request readManyRequest
	err     error
}

// expandReadManyRequests turns a glob into independent bounded reads before
// starting any goroutines. filepath.Glob is sorted, so result ordering stays
// deterministic and therefore KV-cache/replay friendly. Invalid/no-match globs
// remain item-level errors: other requested files still reach the model.
func expandReadManyRequests(baseDir string, requests []readManyRequest) []readManyWork {
	work := make([]readManyWork, 0, len(requests))
	for _, request := range requests {
		if err := validateReadManyRequest(request); err != nil {
			work = append(work, readManyWork{request: request, err: err})
			continue
		}
		if !hasGlobMeta(request.File) {
			work = append(work, readManyWork{request: request})
			continue
		}
		pattern, err := resolveSandboxed(baseDir, request.File)
		if err != nil {
			work = append(work, readManyWork{request: request, err: err})
			continue
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			work = append(work, readManyWork{request: request, err: fmt.Errorf("glob_invalid %s: %w", request.File, err)})
			continue
		}
		if len(matches) == 0 {
			work = append(work, readManyWork{request: request, err: fmt.Errorf("glob_no_matches %s", request.File)})
			continue
		}
		for _, match := range matches {
			resolved, err := resolveSandboxed(baseDir, match)
			if err != nil {
				resolved = match
			}
			matched := request
			matched.File = displayGlobMatch(baseDir, request.File, resolved)
			work = append(work, readManyWork{request: matched, err: err})
		}
	}
	return work
}

func hasGlobMeta(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

func displayGlobMatch(baseDir, pattern, match string) string {
	if filepath.IsAbs(pattern) {
		return match
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return match
	}
	rel, err := filepath.Rel(absBase, match)
	if err != nil {
		return match
	}
	return filepath.ToSlash(rel)
}

// readManyLinesStreaming reads only through the requested final line and
// retains at most maxReadLineChars bytes per line. Unlike fileops.ReadLines it
// never loads the whole file, which matters when several large files are read
// concurrently. ReadSlice lets us consume arbitrarily long lines in bounded
// chunks instead of raising Scanner's token-too-long error.
func readManyLinesStreaming(path string, from, to int) ([]fileops.LineRange, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fileops.FileErr(err, path)
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 32*1024)
	lineNo := 1
	completed := 0
	var content strings.Builder
	omitted := 0
	finishLine := func() {
		completed = lineNo
		lineNo++
	}
	var out []fileops.LineRange
	for {
		fragment, readErr := r.ReadSlice('\n')
		lineDone := !errors.Is(readErr, bufio.ErrBufferFull)
		if lineDone && len(fragment) > 0 && fragment[len(fragment)-1] == '\n' {
			fragment = fragment[:len(fragment)-1]
			if len(fragment) > 0 && fragment[len(fragment)-1] == '\r' {
				fragment = fragment[:len(fragment)-1]
			}
		}
		if lineNo >= from && lineNo <= to {
			keep := maxReadManyLineKeep - content.Len()
			if keep < 0 {
				keep = 0
			}
			if keep > len(fragment) {
				keep = len(fragment)
			}
			content.Write(fragment[:keep])
			omitted += len(fragment) - keep
		}

		if lineDone && (len(fragment) > 0 || readErr == nil) {
			if lineNo >= from && lineNo <= to {
				text := strings.TrimSuffix(strings.ToValidUTF8(content.String(), ""), "\r")
				if omitted > 0 {
					text += fmt.Sprintf(" …[+%d bytes on this line truncated]", omitted)
				}
				out = append(out, fileops.LineRange{Number: lineNo, Content: text})
			}
			finishLine()
			content.Reset()
			omitted = 0
			if completed >= to {
				return out, nil
			}
		}

		switch {
		case readErr == nil, errors.Is(readErr, bufio.ErrBufferFull):
			continue
		case errors.Is(readErr, io.EOF):
			if from > completed {
				return nil, fmt.Errorf("fileops.ReadLines: from=%d exceeds file length %d", from, completed)
			}
			return out, nil
		default:
			return nil, fileops.FileErr(readErr, path)
		}
	}
}

func validateReadManyRequest(r readManyRequest) error {
	if strings.TrimSpace(r.File) == "" {
		return fmt.Errorf("file is empty")
	}
	if len(r.File) > 1024 {
		return fmt.Errorf("file path exceeds 1024 bytes")
	}
	if r.From < 1 || r.To < r.From {
		return fmt.Errorf("invalid range %d-%d", r.From, r.To)
	}
	if r.To-r.From+1 > maxReadManyRange {
		return fmt.Errorf("range %d lines exceeds cap %d", r.To-r.From+1, maxReadManyRange)
	}
	return nil
}

func decodeReadManyRequests(raw json.RawMessage) ([]readManyRequest, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("reads is required")
	}
	var requests []readManyRequest
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &requests); err != nil {
			return nil, fmt.Errorf("invalid reads array: %w", err)
		}
		return requests, nil
	}
	var shorthand string
	if err := json.Unmarshal(raw, &shorthand); err != nil {
		return nil, fmt.Errorf("reads must be an array or shorthand string")
	}
	for _, item := range strings.Split(shorthand, "|") {
		item = strings.TrimSpace(item)
		colon := strings.LastIndexByte(item, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("invalid shorthand %q; want file:from-to", item)
		}
		rangeText := strings.TrimSpace(item[colon+1:])
		dash := strings.IndexByte(rangeText, '-')
		if dash <= 0 {
			return nil, fmt.Errorf("invalid shorthand range %q", rangeText)
		}
		from, errFrom := strconv.Atoi(strings.TrimSpace(rangeText[:dash]))
		to, errTo := strconv.Atoi(strings.TrimSpace(rangeText[dash+1:]))
		if errFrom != nil || errTo != nil {
			return nil, fmt.Errorf("invalid shorthand range %q", rangeText)
		}
		requests = append(requests, readManyRequest{File: strings.TrimSpace(item[:colon]), From: from, To: to})
	}
	return requests, nil
}
