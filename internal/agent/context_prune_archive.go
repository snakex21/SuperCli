package agent

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/llm"
	"supercli/internal/tools/core"
)

// A single bounded archive keeps an entire pruning batch inspectable without
// filling the 32-entry output cache with one entry per small result. It is only
// materialized after the gain gate accepts pruning, never on ordinary reads.
const pruneArchiveMaxBytes = 4 * 1024 * 1024
const pruneArchiveHandle = "out_00000000000000000000000000000000"

type pruneVictim struct {
	index         int
	marker        string
	archiveHeader string
	archiveOffset int
	archiveLimit  int
}

type pruneArchive struct{ bytes int }

func (a *pruneArchive) plan(v *pruneVictim, content string, resultTokens, markerTokens int) (int, bool) {
	if content == "" || !strings.HasSuffix(v.marker, "; details omitted]") {
		return markerTokens, true
	}
	header := fmt.Sprintf("\n[Historical tool result, message %d]\n", v.index)
	size := len(header) + len(content) + 1
	if size > pruneArchiveMaxBytes-a.bytes {
		return markerTokens, true // retain the existing bounded fallback
	}
	v.archiveHeader = header
	v.archiveOffset = a.bytes + len(header)
	v.archiveLimit = min(len(content), 8192) // existing read_output byte cap
	markerTokens = llm.EstimateMessageTokens(llm.Message{Role: llm.RoleTool, Content: v.archiveMarker(pruneArchiveHandle)})
	if resultTokens <= 2*markerTokens {
		return 0, false // keeping a short result beats replacing it with a reference
	}
	a.bytes += size
	return markerTokens, true
}

func (v pruneVictim) archiveMarker(handle string) string {
	if v.archiveHeader == "" || handle == "" {
		return v.marker
	}
	return strings.TrimSuffix(v.marker, "details omitted]") + fmt.Sprintf("read_output {\"handle\":%q,\"offset\":%d,\"limit\":%d}]", handle, v.archiveOffset, v.archiveLimit)
}

func (a pruneArchive) save(ctx context.Context, l *Loop, victims []pruneVictim) string {
	if a.bytes == 0 || l.registry == nil {
		return ""
	}
	var archive strings.Builder
	archive.Grow(a.bytes)
	for _, v := range victims {
		if v.archiveHeader == "" {
			continue
		}
		archive.WriteString(v.archiveHeader)
		archive.WriteString(l.Messages[v.index].Content)
		archive.WriteByte('\n')
	}
	if l.toolOutputs != nil {
		ctx = core.WithOutputPersistence(ctx, l.toolOutputs)
	}
	// Reuse the same bounded memory/persistence path as tool attachments. A failed
	// save still keeps the memory reference; unavailable retention yields no handle.
	content := l.registry.ModelResultContentContext(ctx, "pruned_tool_results", core.Result{RetainedText: archive.String()})
	return core.StoredOutputHandle(content)
}
