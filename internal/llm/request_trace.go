package llm

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

const (
	requestTraceBlock      = 1024
	requestTraceMaxBody    = 4 << 20
	requestTraceMaxStreams = 32
)

// RequestTrace describes HTTP attempts within one Complete invocation. Timing
// and provider-reported token/cache counters remain on CallStat. It deliberately
// contains no request text, URL, raw routing key, credential or body digest.
// First/Last refer to client attempts, not server-confirmed cache admissions.
type RequestTrace struct {
	Version  int                  `json:"version"`
	Attempts int                  `json:"attempts"`
	First    RequestAttemptTrace  `json:"first"`
	Last     *RequestAttemptTrace `json:"last,omitempty"`
}

// RequestAttemptTrace compares serialized wire JSON, NOT the backend's tokenized
// cache prefix. Changed offsets are an inclusive byte range (1 KiB precision).
// An append has an exact offset. Scope/sequence are process-local; restart or
// LRU eviction starts a new baseline. No fingerprints are persisted.
type RequestAttemptTrace struct {
	Scope                        string `json:"scope,omitempty"`
	Sequence                     uint64 `json:"sequence,omitempty"`
	PreviousSequence             uint64 `json:"previous_sequence,omitempty"`
	State                        string `json:"state"`
	Unavailable                  string `json:"unavailable,omitempty"`
	BodyBytes                    int64  `json:"body_bytes"`
	PreviousBytes                int64  `json:"previous_bytes,omitempty"`
	BlockBytes                   int    `json:"block_bytes,omitempty"`
	FirstDiffFrom                *int64 `json:"first_diff_from,omitempty"`
	FirstDiffTo                  *int64 `json:"first_diff_to,omitempty"`
	CacheKeyPresent              bool   `json:"cache_key_present"`
	CacheKeyChanged              *bool  `json:"cache_key_changed,omitempty"`
	SessionHeaderPresent         bool   `json:"session_header_present"`
	SessionHeaderMatchesCacheKey *bool  `json:"session_header_matches_cache_key,omitempty"`
}

type requestTraceContextKey struct{}
type requestTraceCall struct {
	mu     sync.Mutex
	owner  [32]byte
	scoped bool
	trace  RequestTrace
}
type requestTraceBaseline struct {
	owner    [32]byte
	sequence uint64
	bytes    int
	blocks   [][32]byte
	cacheKey [32]byte
}
type requestTraceTracker struct {
	mu       sync.Mutex
	secret   [32]byte
	ready    bool
	sequence uint64
	streams  map[[32]byte]requestTraceBaseline
}

var wireRequestTraces = newRequestTraceTracker()

func newRequestTraceTracker() *requestTraceTracker {
	t := &requestTraceTracker{streams: make(map[[32]byte]requestTraceBaseline)}
	_, err := rand.Read(t.secret[:])
	t.ready = err == nil
	return t
}

func (t *requestTraceTracker) digest(parts ...[]byte) [32]byte {
	h := hmac.New(sha256.New, t.secret[:])
	var size [8]byte
	for _, p := range parts {
		binary.LittleEndian.PutUint64(size[:], uint64(len(p)))
		h.Write(size[:])
		h.Write(p)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func beginRequestTrace(ctx context.Context, m *metered, purpose string) (context.Context, *requestTraceCall) {
	session := openCodeSessionFromContext(ctx)
	scope, _ := ctx.Value(promptCacheScopeKey{}).(string)
	connection := m.connectionKey
	if connection == "" {
		// Unconfigured/test wrappers must not share another provider's baseline.
		connection = fmt.Sprintf("%p", m)
	}
	c := &requestTraceCall{scoped: session != "", trace: RequestTrace{Version: 1,
		First: RequestAttemptTrace{State: "unavailable", Unavailable: "no_http_attempt", BodyBytes: -1}}}
	c.owner = wireRequestTraces.digest([]byte(session), []byte(scope), []byte(purpose), []byte(connection), []byte(m.inner.Name()))
	return context.WithValue(ctx, requestTraceContextKey{}, c), c
}

func (c *requestTraceCall) snapshot() *RequestTrace {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.trace
	return &v
}

func (c *requestTraceCall) record(v RequestAttemptTrace) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trace.Attempts++
	if c.trace.Attempts == 1 {
		c.trace.First = v
	} else {
		c.trace.Last = &v
	}
}

// observeWireRequest only reads an independent replay reader. The outgoing
// Body, GetBody, headers, context and JSON bytes are never rewritten. Without
// a metered collector it does no work (catalog/auth requests are not traced).
func observeWireRequest(req *http.Request) {
	c, _ := req.Context().Value(requestTraceContextKey{}).(*requestTraceCall)
	if c == nil {
		return
	}
	v := RequestAttemptTrace{State: "unavailable", BodyBytes: req.ContentLength}
	reason := ""
	switch {
	case !wireRequestTraces.ready:
		reason = "entropy_unavailable"
	case !c.scoped:
		reason = "no_session"
	case req.Method != http.MethodPost || req.GetBody == nil:
		reason = "non_replayable"
	case req.ContentLength > requestTraceMaxBody:
		reason = "body_limit"
	}
	if reason != "" {
		v.Unavailable = reason
		wireRequestTraces.forget(c.owner)
		c.record(v)
		return
	}
	r, err := req.GetBody()
	if err != nil {
		v.Unavailable = "read_error"
	} else {
		body, readErr := io.ReadAll(io.LimitReader(r, requestTraceMaxBody+1))
		r.Close()
		switch {
		case readErr != nil:
			v.Unavailable = "read_error"
		case len(body) > requestTraceMaxBody:
			v.Unavailable = "body_limit"
		default:
			var routing struct {
				Model    string `json:"model"`
				CacheKey string `json:"prompt_cache_key"`
			}
			if json.Unmarshal(body, &routing) != nil {
				v.Unavailable = "invalid_json"
			} else {
				// Account/endpoint/model are isolated only via process-secret HMAC;
				// none of these strings or their fingerprints leave this function.
				stream := wireRequestTraces.digest(c.owner[:], []byte(req.URL.Host), []byte(req.URL.Path), []byte(routing.Model), []byte(req.Header.Get("chatgpt-account-id")))
				v = wireRequestTraces.compare(c.owner, stream, body, routing.CacheKey)
				v.SessionHeaderPresent = req.Header.Get("session_id") != ""
				if v.SessionHeaderPresent && v.CacheKeyPresent {
					equal := req.Header.Get("session_id") == routing.CacheKey
					v.SessionHeaderMatchesCacheKey = &equal
				}
			}
		}
	}
	if v.Unavailable != "" {
		wireRequestTraces.forget(c.owner)
	}
	c.record(v)
}

func (t *requestTraceTracker) forget(owner [32]byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, v := range t.streams {
		if v.owner == owner {
			delete(t.streams, k)
		}
	}
}

func (t *requestTraceTracker) compare(owner, stream [32]byte, body []byte, cacheKey string) RequestAttemptTrace {
	blocks := make([][32]byte, (len(body)+requestTraceBlock-1)/requestTraceBlock)
	h := hmac.New(sha256.New, t.secret[:])
	var size [8]byte
	for i, start := 0, 0; start < len(body); i, start = i+1, start+requestTraceBlock {
		block := body[start:min(start+requestTraceBlock, len(body))]
		h.Reset()
		binary.LittleEndian.PutUint64(size[:], uint64(len(block)))
		h.Write(size[:])
		h.Write(block)
		h.Sum(blocks[i][:0])
	}
	keyDigest := t.digest([]byte(cacheKey))
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sequence++
	v := RequestAttemptTrace{Scope: hex.EncodeToString(stream[:16]), Sequence: t.sequence,
		State: "first", BodyBytes: int64(len(body)), BlockBytes: requestTraceBlock, CacheKeyPresent: cacheKey != ""}
	if prev, ok := t.streams[stream]; ok {
		v.PreviousSequence, v.PreviousBytes = prev.sequence, int64(prev.bytes)
		keyChanged := prev.cacheKey != keyDigest
		v.CacheKeyChanged = &keyChanged
		full := min(prev.bytes, len(body)) / requestTraceBlock
		matched := 0
		for matched < full && prev.blocks[matched] == blocks[matched] {
			matched++
		}
		equalPrefix := matched == full
		if equalPrefix && len(body) >= prev.bytes && prev.bytes%requestTraceBlock != 0 {
			start := full * requestTraceBlock
			equalPrefix = t.digest(body[start:prev.bytes]) == prev.blocks[full]
		}
		switch {
		case equalPrefix && len(body) == prev.bytes:
			v.State = "identical"
		case equalPrefix && len(body) > prev.bytes:
			v.State = "append"
			at := int64(prev.bytes)
			v.FirstDiffFrom, v.FirstDiffTo = &at, &at
		default:
			v.State = "changed"
			from := int64(matched * requestTraceBlock)
			to := int64(min((matched+1)*requestTraceBlock-1, min(prev.bytes, len(body))))
			v.FirstDiffFrom, v.FirstDiffTo = &from, &to
		}
	}
	if _, exists := t.streams[stream]; !exists && len(t.streams) >= requestTraceMaxStreams {
		var oldest [32]byte
		seq := ^uint64(0)
		for k, baseline := range t.streams {
			if baseline.sequence < seq {
				oldest, seq = k, baseline.sequence
			}
		}
		delete(t.streams, oldest)
	}
	t.streams[stream] = requestTraceBaseline{owner: owner, sequence: v.Sequence, bytes: len(body), blocks: blocks, cacheKey: keyDigest}
	return v
}

// SafeRequestTrace validates the small, closed persistence schema. Never store
// an arbitrary metadata map or a raw request fragment alongside usage.
func SafeRequestTrace(v *RequestTrace) *RequestTrace {
	if v == nil || v.Version != 1 || v.Attempts < 0 || !safeRequestAttempt(v.First) || (v.Last != nil && !safeRequestAttempt(*v.Last)) {
		return nil
	}
	out := *v
	return &out
}

func safeRequestAttempt(v RequestAttemptTrace) bool {
	if v.Scope != "" {
		b, err := hex.DecodeString(v.Scope)
		if err != nil || len(b) != 16 {
			return false
		}
	}
	switch v.State {
	case "first", "identical", "append", "changed":
		return v.Unavailable == "" && v.Scope != "" && v.Sequence > 0 && v.BlockBytes == requestTraceBlock
	case "unavailable":
		switch v.Unavailable {
		case "no_http_attempt", "entropy_unavailable", "no_session", "non_replayable", "body_limit", "read_error", "invalid_json":
			return v.Scope == ""
		}
	}
	return false
}
