package files

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"supercli/internal/tools/fileops"
)

const (
	maxReadManyRequests = 12
	maxReadManyRange    = 300
	maxReadManyBytes    = 32 * 1024
	maxReadManyItem     = 8 * 1024
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
			"Use 'file:from-to | file:from-to'; a bare file defaults to lines 1-300; globs are allowed. " +
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

	outcomes := make([]readManyOutcome, len(work))
	type readGroup struct {
		file    string
		indices []int
	}
	var groups []readGroup
	groupForFile := make(map[string]int, len(work))
	for i, item := range work {
		request := item.request
		outcomes[i].request = request
		if item.err != nil {
			outcomes[i].err = item.err
			continue
		}
		if err := ctx.Err(); err != nil {
			outcomes[i].err = err
			continue
		}
		if err := validateReadManyRequest(request); err != nil {
			outcomes[i].err = err
			continue
		}
		// Cap before grouping; every returned range still has its own bound.
		if request.To-request.From >= maxReadManyRange {
			request.To = request.From + maxReadManyRange - 1
			outcomes[i].request = request
		}
		group, found := groupForFile[request.File]
		if !found {
			group = len(groups)
			groupForFile[request.File] = group
			groups = append(groups, readGroup{file: request.File})
		}
		groups[group].indices = append(groups[group].indices, i)
	}

	finish := func(i int, path string, lines []fileops.LineRange, eof bool, err error) {
		if err != nil {
			outcomes[i].err = suggestReadFile(ctx, path, err)
			return
		}
		for j := range lines {
			lines[j].Content = strings.TrimSuffix(lines[j].Content, "\r")
		}
		outcomes[i].text = renderLinesWithEOF(lines, eof)
		request, requestedTo := outcomes[i].request, work[i].request.To
		if request.To < requestedTo && !eof {
			outcomes[i].text += fmt.Sprintf("[range capped at %d lines; requested lines %d-%d not read]\n",
				maxReadManyRange, request.To+1, requestedTo)
		}
	}
	var wg sync.WaitGroup
	for _, group := range groups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := resolveSandboxed(t.BaseDir, group.file)
			if err != nil {
				for _, i := range group.indices {
					outcomes[i].err = err
				}
				return
			}
			if len(group.indices) == 1 {
				i := group.indices[0]
				request := outcomes[i].request
				lines, eof, err := fileops.ReadLinesBoundedWithEOF(ctx, path, request.From, request.To, maxReadLineKeep)
				finish(i, path, lines, eof, err)
				return
			}
			spans := make([]fileops.LineSpan, len(group.indices))
			for j, i := range group.indices {
				spans[j] = fileops.LineSpan{From: outcomes[i].request.From, To: outcomes[i].request.To}
			}
			// One open/scan per repeated file, with no cache across tool calls.
			for j, result := range fileops.ReadRangesBoundedWithEOF(ctx, path, spans, maxReadLineKeep) {
				finish(group.indices[j], path, result.Lines, result.EOF, result.Err)
			}
		}()
	}
	wg.Wait()

	return renderReadMany(outcomes), nil
}

type readManyOutcome struct {
	request readManyRequest
	text    string
	err     error
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
		for i := range requests {
			defaultReadManyRange(&requests[i])
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
		if colon > 0 {
			rangeText := strings.TrimSpace(item[colon+1:])
			dash := strings.IndexByte(rangeText, '-')
			if dash > 0 {
				from, errFrom := strconv.Atoi(strings.TrimSpace(rangeText[:dash]))
				to, errTo := strconv.Atoi(strings.TrimSpace(rangeText[dash+1:]))
				if errFrom == nil && errTo == nil {
					requests = append(requests, readManyRequest{File: strings.TrimSpace(item[:colon]), From: from, To: to})
					continue
				}
			}
		}
		request := readManyRequest{File: item}
		defaultReadManyRange(&request)
		requests = append(requests, request)
	}
	return requests, nil
}

func defaultReadManyRange(request *readManyRequest) {
	if request.From == 0 && request.To == 0 {
		request.From = 1
		request.To = maxReadManyRange
	} else if request.From > 0 && request.To == 0 {
		request.To = math.MaxInt
		if request.From <= math.MaxInt-maxReadManyRange+1 {
			request.To = request.From + maxReadManyRange - 1
		}
	}
}
