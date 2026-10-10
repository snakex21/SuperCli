package checkpoint

import (
	"math"
	"strings"
)

func storeUsageMultiply(a, b int64) (int64, error) {
	if a < 0 || b < 0 || (a != 0 && b > math.MaxInt64/a) {
		return 0, ErrStoreInventory
	}
	return a * b, nil
}

// Bound ordinary current Go/Git zlib object writers conservatively without
// recompression or object enumeration. Stored DEFLATE blocks cost at most input
// plus six bytes of framing/alignment; each nonempty block consumes at least
// one byte. Allow one final empty block and six bytes of zlib header/trailer:
// raw + 6*(raw+1) + 6 = 7*raw+12. Current writers may select a smaller compressed
// block, and neither calls Flush per byte. This intentionally coarse metadata
// bound can be replaced by already observed new compressed object sizes.
func StoreUsageObjectBound(payloadBytes, objects int64) (int64, error) {
	if objects < 0 || payloadBytes < 0 || (objects == 0 && payloadBytes != 0) {
		return 0, ErrStoreInventory
	}
	// "commit" (6) + space (1) + int64 decimal size (<=19) + NUL (1).
	headers, err := storeUsageMultiply(objects, 27)
	if err != nil {
		return 0, err
	}
	raw, err := retentionAdd(payloadBytes, headers)
	if err != nil {
		return 0, err
	}
	encoded, err := storeUsageMultiply(raw, 7)
	if err != nil {
		return 0, err
	}
	framing, err := storeUsageMultiply(objects, 12)
	if err != nil {
		return 0, err
	}
	return retentionAdd(encoded, framing)
}

// Accumulate paths ALREADY traversed by snapshot/narrowing. Duplicate directory
// names/paths intentionally overcount, avoiding a retained trie or history map.
// Include protected base paths already read by checkExpandedSnapshot; selected
// new paths alone do not bound an expanded/full snapshot's trees.
type StoreUsageTreeShape struct {
	PayloadBytes int64
	Directories  int64
}

func (s *StoreUsageTreeShape) AddPath(path string) error {
	if !validNarrowPath(path) {
		return ErrStoreInventory
	}
	segments := int64(strings.Count(path, "/") + 1)
	// Regular tree entry: mode(6)+space(1)+name+NUL(1)+binary SHA1(20).
	// Directory mode is shorter; each duplicate ancestor still counts in full.
	overhead, err := storeUsageMultiply(segments, 28)
	if err != nil {
		return err
	}
	payload, err := retentionAdd(int64(len(path))-segments+1, overhead)
	if err != nil {
		return err
	}
	payload, err = retentionAdd(s.PayloadBytes, payload)
	if err != nil {
		return err
	}
	dirs, err := retentionAdd(s.Directories, segments-1)
	if err != nil {
		return err
	}
	s.PayloadBytes, s.Directories = payload, dirs
	return nil
}

func (s StoreUsageTreeShape) Bound(commitPayloadBytes int64) (int64, error) {
	trees, err := retentionAdd(s.Directories, 1) // The root tree also exists.
	if err != nil {
		return 0, err
	}
	objects, err := retentionAdd(trees, 1) // One parentless snapshot commit.
	if err != nil {
		return 0, err
	}
	payload, err := retentionAdd(s.PayloadBytes, commitPayloadBytes)
	if err != nil {
		return 0, err
	}
	return StoreUsageObjectBound(payload, objects)
}

// Loose SHA1 ref content is exactly 40 hexadecimal bytes plus newline. Count
// every update conservatively, even overwrite/dedup/no-op, never subtract a
// deletion. Reflogs are separate: charge a measured bound or RequireCensus.
func StoreUsageRefBound(writes int64) (int64, error) {
	return storeUsageMultiply(writes, 41)
}
