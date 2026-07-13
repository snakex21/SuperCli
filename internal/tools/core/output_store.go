package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	// Successful tool output below this size stays byte-identical. Large
	// results become a useful head/tail preview plus an in-memory handle.
	outputInlineBytes = 8 * 1024
	outputPreviewHead = 3 * 1024
	outputPreviewTail = 1 * 1024
	outputReadDefault = 4 * 1024
	outputReadMax     = 8 * 1024
	outputStoreItems  = 32
	outputStoreBytes  = 16 * 1024 * 1024
)

// OutputStore keeps large successful tool results out of the model context
// without throwing them away. It is deliberately process-local: a model can
// inspect another chunk during the current run, while persisted session
// history keeps only the bounded preview. Memory and entry count are bounded
// with a tiny LRU; tool outputs are held as strings without copying their data.
type OutputStore struct {
	mu      sync.Mutex
	entries map[string]*storedOutput
	bytes   int
	seq     uint64
	tick    uint64
}

type storedOutput struct {
	text string
	used uint64
}

func NewOutputStore() *OutputStore {
	return &OutputStore{entries: make(map[string]*storedOutput)}
}

// Compact returns text unchanged in the dominant small-result case. Large
// text is retained and replaced with a 4 KiB head/tail preview. read_output is
// exempt so fetching a chunk can never recursively create another handle.
func (s *OutputStore) Compact(toolName, text string) string {
	if s == nil || toolName == "read_output" || len(text) <= outputInlineBytes {
		return text
	}
	handle, ok := s.put(text)
	if !ok {
		return HeadTail(text, outputPreviewHead, outputPreviewTail) +
			fmt.Sprintf("\n[output preview only: %d bytes; result exceeded the in-memory store limit]", len(text))
	}
	preview := HeadTail(text, outputPreviewHead, outputPreviewTail)
	return fmt.Sprintf("[large tool output: %d bytes; preview follows; handle=%s]\n%s\n"+
		"[inspect more with read_output {\"handle\":%q,\"offset\":4096,\"limit\":4096}; handle is valid during this run]",
		len(text), handle, preview, handle)
}

func (s *OutputStore) put(text string) (string, bool) {
	if len(text) > outputStoreBytes {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]*storedOutput)
	}
	s.seq++
	s.tick++
	handle := fmt.Sprintf("out_%06d", s.seq)
	s.entries[handle] = &storedOutput{text: text, used: s.tick}
	s.bytes += len(text)
	s.evictLocked()
	return handle, true
}

func (s *OutputStore) evictLocked() {
	for len(s.entries) > outputStoreItems || s.bytes > outputStoreBytes {
		var oldestKey string
		var oldestTick uint64
		for key, entry := range s.entries {
			if oldestKey == "" || entry.used < oldestTick {
				oldestKey, oldestTick = key, entry.used
			}
		}
		if oldestKey == "" {
			return
		}
		s.bytes -= len(s.entries[oldestKey].text)
		delete(s.entries, oldestKey)
	}
}

func (s *OutputStore) read(handle string, offset, limit int) (string, int, int, error) {
	if s == nil {
		return "", 0, 0, fmt.Errorf("output store unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[handle]
	if !ok {
		return "", 0, 0, fmt.Errorf("unknown or expired output handle %q", handle)
	}
	if offset < 0 || offset > len(entry.text) {
		return "", 0, 0, fmt.Errorf("offset %d outside output size %d", offset, len(entry.text))
	}
	if limit <= 0 {
		limit = outputReadDefault
	}
	if limit > outputReadMax {
		limit = outputReadMax
	}
	start := runeStartForward(entry.text, offset)
	end := start + limit
	if end > len(entry.text) {
		end = len(entry.text)
	} else {
		end = runeStartBackward(entry.text, end)
	}
	s.tick++
	entry.used = s.tick
	return entry.text[start:end], start, len(entry.text), nil
}

func runeStartForward(s string, i int) int {
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return i
}

func runeStartBackward(s string, i int) int {
	for i > 0 && i < len(s) && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

type readOutputArgs struct {
	Handle string `json:"handle"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// ReadOutputTool returns a compact slice from a previously stored result. It
// has a small, flat schema because it is a thin-core escape hatch: paying a
// few schema tokens is preferable to injecting tens of thousands of result
// tokens or forcing the model to repeat the original expensive operation.
func (s *OutputStore) ReadOutputTool() Tool {
	return Tool{
		Name:        "read_output",
		Description: "Read another byte range from a large tool result using its output handle.",
		ReadOnly:    true,
		Schema:      `{"type":"object","properties":{"handle":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer","maximum":8192}},"required":["handle"]}`,
		Fn: func(_ context.Context, raw json.RawMessage) (Result, error) {
			var args readOutputArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return Result{Err: fmt.Errorf("read_output: bad args: %w", err)}, nil
			}
			args.Handle = strings.TrimSpace(args.Handle)
			if args.Handle == "" {
				return Result{Err: fmt.Errorf("read_output: handle is required")}, nil
			}
			chunk, start, total, err := s.read(args.Handle, args.Offset, args.Limit)
			if err != nil {
				return Result{Err: fmt.Errorf("read_output: %w", err)}, nil
			}
			end := start + len(chunk)
			var b strings.Builder
			fmt.Fprintf(&b, "[stored output %s bytes %d:%d of %d]\n", args.Handle, start, end, total)
			b.WriteString(chunk)
			if end < total {
				fmt.Fprintf(&b, "\n[next offset: %d]", end)
			} else {
				b.WriteString("\n[end of stored output]")
			}
			return Result{Text: b.String()}, nil
		},
	}
}
