package core

import (
	"strconv"
	"strings"
)

// StoredOutputHandle finds the outer reference emitted by CompactContext or
// retainBehind. Inspect only the bounded envelope, never arbitrary log contents.
// Keeping this reference during context pruning costs no output read or write.
func StoredOutputHandle(text string) string {
	const envelopeBytes = 256
	tail := text
	if len(tail) > envelopeBytes {
		tail = tail[len(tail)-envelopeBytes:]
	}
	if i := strings.LastIndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}
	if rest, ok := strings.CutPrefix(tail, "[stored tool output: "); ok {
		if size, after, ok := strings.Cut(rest, " bytes; handle="); ok && outputReferenceSize(size) {
			handle, suffix, ok := strings.Cut(after, "; read_output ")
			if ok && validOutputReference(handle) && strings.HasPrefix(suffix, "{\"handle\":"+strconv.Quote(handle)+"}; ") && strings.HasSuffix(suffix, "]") {
				return handle
			}
		}
	}

	head := text
	if len(head) > envelopeBytes {
		head = head[:envelopeBytes]
	}
	head, _, _ = strings.Cut(head, "\n")
	if rest, ok := strings.CutPrefix(head, "[large tool output: "); ok && strings.HasSuffix(rest, "]") {
		size, handle, ok := strings.Cut(strings.TrimSuffix(rest, "]"), " bytes; preview follows; handle=")
		if ok && outputReferenceSize(size) && validOutputReference(handle) {
			return handle
		}
	}
	return ""
}

func outputReferenceSize(size string) bool {
	n, err := strconv.Atoi(size)
	return err == nil && n > 0
}

func validOutputReference(handle string) bool {
	hex, ok := strings.CutPrefix(handle, "out_")
	// Current handles have 32 hex digits; older transcripts used shorter IDs.
	if !ok || len(hex) < 6 || len(hex) > 32 {
		return false
	}
	for _, c := range hex {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
