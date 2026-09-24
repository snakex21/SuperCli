package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	// ModelOutputInlineBytes is the threshold for compacting successful output.
	ModelOutputInlineBytes = outputInlineBytes
	// A bounded batch replaces multiple independent reads. Avoid truncating a
	// modest batch only because their combined output exceeds a single read.
	ModelReadBatchInlineBytes = 12 * 1024
	// ModelOutputPreviewBytes is shared by structured and generic previews.
	ModelOutputPreviewBytes = outputPreviewHead + outputPreviewTail
	outputPreviewHead       = 3 * 1024
	outputPreviewTail       = 1 * 1024
	outputReadDefault       = 4 * 1024
	outputReadMax           = 8 * 1024
	outputStoreItems        = 32
	outputStoreBytes        = 16 * 1024 * 1024
)

// OutputStore keeps large tool results out of the model context
// without expanding the prompt. A tiny LRU holds active results; optional
// persistence serves the same opaque handles on demand after a fresh loop or
// restart. History carries only previews. Ordinary small results incur no I/O.
type OutputStore struct {
	mu      sync.Mutex
	entries map[string]*storedOutput
	loading map[string]*outputLoad
	bytes   int
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
	return s.CompactContext(context.Background(), toolName, text)
}

func (s *OutputStore) CompactContext(ctx context.Context, toolName, text string) string {
	if s == nil || toolName == "read_output" || len(text) <= modelInlineLimit(toolName) {
		return text
	}
	handle, ok, saved := s.retain(ctx, text)
	if !ok {
		return HeadTail(text, outputPreviewHead, outputPreviewTail) +
			fmt.Sprintf("\n[output preview only: %d bytes; result could not be retained within the store limit]", len(text))
	}
	preview := HeadTail(text, outputPreviewHead, outputPreviewTail)
	offset, limit := nextChunkHint(len(text))
	return fmt.Sprintf("[large tool output: %d bytes; preview follows; handle=%s]\n%s\n"+
		"[inspect more with read_output {\"handle\":%q,\"offset\":%d,\"limit\":%d}; %s]",
		len(text), handle, preview, handle, offset, limit, outputLifetime(saved))
}

// ModelContent preserves the usual failure summary (including its exit status)
// and saves any larger diagnostic text it omitted. A failed build must be just
// as inspectable as a successful one, without repeating the command.
func (s *OutputStore) ModelContent(toolName string, result Result) string {
	return s.ModelContentContext(context.Background(), toolName, result)
}

func (s *OutputStore) ModelContentContext(ctx context.Context, toolName string, result Result) string {
	if s != nil && toolName != "read_output" && result.Err == nil && result.ModelPreview != "" {
		preview := result.ModelPreview
		if len(preview) > ModelOutputPreviewBytes {
			// Reserve space for HeadTail's omission marker as well.
			preview = HeadTail(preview, outputPreviewHead-64, outputPreviewTail-32)
		}
		retained := result.RetainedText
		if retained == "" {
			retained = result.Text
		}
		return s.retainBehind(ctx, preview, retained)
	}
	if s != nil && toolName != "read_output" && result.RetainedText != "" {
		content := result.ModelContent()
		if result.Err == nil && len(content) > modelInlineLimit(toolName) {
			content = HeadTail(content, outputPreviewHead, outputPreviewTail)
		}
		return s.retainBehind(ctx, content, result.RetainedText)
	}
	if result.Err == nil {
		return s.CompactContext(ctx, toolName, result.Text)
	}
	content := result.ModelContent()
	if s == nil || toolName == "read_output" || len(result.Text) <= ModelContentTailBytes || strings.Contains(content, strings.TrimSpace(result.Text)) {
		return content
	}
	return s.retainBehind(ctx, content, result.Text)
}

func (s *OutputStore) retainBehind(ctx context.Context, content, text string) string {
	handle, ok, saved := s.retain(ctx, text)
	if !ok {
		return content + fmt.Sprintf("\n[additional tool output not retained: %d bytes; store unavailable or limit exceeded]", len(text))
	}
	return content + fmt.Sprintf("\n[stored tool output: %d bytes; handle=%s; read_output {\"handle\":%q}; %s]", len(text), handle, handle, outputLifetime(saved))
}

// nextChunkHint picks the offset/limit the model is told to ask for. Models
// follow this hint literally, so every byte it leaves on the table costs a
// whole extra round trip. The offset resumes exactly where the preview's head
// stopped — 4096 used to skip a kilobyte nobody had seen — and the limit is
// the largest chunk read_output returns, clamped to what is left so the
// hint never points past the end. Larger requests use the same output cap.
func nextChunkHint(total int) (offset, limit int) {
	offset = outputPreviewHead
	if limit = total - offset; limit > outputReadMax {
		limit = outputReadMax
	}
	return offset, limit
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
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", false
	}
	handle := "out_" + hex.EncodeToString(id[:])
	s.tick++
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
	return s.readContext(context.Background(), handle, offset, limit)
}

func (s *OutputStore) readContext(ctx context.Context, handle string, offset, limit int) (string, int, int, error) {
	text, err := s.load(ctx, handle)
	if err != nil {
		return "", 0, 0, err
	}
	if offset < 0 || offset > len(text) {
		return "", 0, 0, fmt.Errorf("offset %d outside output size %d", offset, len(text))
	}
	if limit <= 0 {
		limit = outputReadDefault
	}
	if limit > outputReadMax {
		limit = outputReadMax
	}
	start := runeStartForward(text, offset)
	end := start + limit
	if end > len(text) {
		end = len(text)
	} else {
		end = runeStartBackward(text, end)
	}
	return text[start:end], start, len(text), nil
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
	Query  string `json:"query"`
}

// ReadOutputTool returns a compact slice from a previously stored result. It
// has a small, flat schema because it is a thin-core escape hatch: paying a
// few schema tokens is preferable to injecting tens of thousands of result
// tokens or forcing the model to repeat the original expensive operation.
func (s *OutputStore) ReadOutputTool() Tool {
	return Tool{
		Name:        "read_output",
		Description: "Read a byte range or search a stored tool result. query is a case-sensitive literal; offset resumes reading or searching.",
		ReadOnly:    true,
		Schema:      `{"type":"object","properties":{"handle":{"type":"string"},"offset":{"type":"integer"},"limit":{"type":"integer","description":"Bytes; capped at 8192"},"query":{"type":"string","maxLength":512}},"required":["handle"]}`,
		Fn: func(ctx context.Context, raw json.RawMessage) (Result, error) {
			var args readOutputArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return Result{Err: fmt.Errorf("read_output: bad args: %w", err)}, nil
			}
			args.Handle = strings.TrimSpace(args.Handle)
			if args.Handle == "" {
				return Result{Err: fmt.Errorf("read_output: handle is required")}, nil
			}
			if args.Query != "" {
				text, err := s.search(ctx, args)
				if err != nil {
					return Result{Err: fmt.Errorf("read_output: %w", err)}, nil
				}
				return Result{Text: text}, nil
			}
			chunk, start, total, err := s.readContext(ctx, args.Handle, args.Offset, args.Limit)
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

// Other tool families keep their existing output limits.
func modelInlineLimit(toolName string) int {
	if toolName == "read_many" {
		return ModelReadBatchInlineBytes
	}
	return outputInlineBytes
}
