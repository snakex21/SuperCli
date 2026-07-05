package llm

// Warm KV cache across SuperCli sessions via llama.cpp's slot
// save/restore API:
//
//	POST {server}/slots/{id_slot}?action=save     body {"filename": "..."}
//	POST {server}/slots/{id_slot}?action=restore  body {"filename": "..."}
//	POST {server}/slots/{id_slot}?action=erase
//
// The endpoints live at the SERVER ROOT (not under /v1) and only work
// when the server was started with `--slot-save-path <dir>`; without it
// llama.cpp answers HTTP 501 ("not_supported_error"). An invalid
// filename or slot id is HTTP 400. Verified against
// tools/server/server-context.cpp (post_slots / handle_slots_save) and
// tools/server/README.md.
//
// Consistency: restore is ALWAYS safe. llama.cpp matches the incoming
// prompt against the restored KV by longest common prefix and re-evals
// from the first divergent token. When the client resends an identical
// append-only history (SuperCli's prompt front is byte-stable and the
// freshness stamp trails the prompt — see system_demote.go), restore
// yields a full cache hit for the whole history; when the history
// changed (compaction, summarized resume, model swap) the restore is
// merely less profitable, never wrong.
//
// Model caveat (measured live, 2026-07-05): the full-history hit holds
// for DENSE-attention models (e.g. Ministral-3-3B: resume eval dropped
// 1712 -> 88). HYBRID models with recurrent/linear-attention layers
// (Qwen3.5 family) cannot roll their state back to a divergence point
// without a context checkpoint, and llama_state_seq_save_file stores
// only the sequence KV + tokens — no checkpoints. A resumed prompt is
// by construction a strict prefix of the saved state (the saved slot
// ends with the last generated answer), so on hybrids the restore
// currently degrades to a clean full re-eval (cache_n=0) — safe, just
// not profitable until llama.cpp persists checkpoints in slot files.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// SlotCache drives slot KV save/restore against one llama.cpp server.
// It is capability-gated twice:
//
//   - Construction: NewSlotCache returns nil for cloud/public base URLs
//     (they do not implement /slots and must never be probed). A config
//     override (slot_cache = true|false) forces it on for a self-hosted
//     server behind a public address, or off entirely.
//   - First use: the first Save/Restore doubles as the probe. Any
//     failure (501 without --slot-save-path, 404 on an old server,
//     transport error) disables the SlotCache for the rest of the
//     process — zero retries, zero UX noise. Callers log the first
//     error at debug level and move on.
//
// Slot id: always 0. SuperCli deliberately does not pin id_slot on
// completions (see isLocalBaseURL notes), but a fresh server assigns
// the first request to slot 0 and longest-prefix routing keeps a
// mostly-serial session there. Saving the wrong slot only costs the
// benefit (restore diverges at token 0 and the server re-evals), never
// correctness.
type SlotCache struct {
	root string // scheme://host[:port], no path
	http *http.Client

	mu       sync.Mutex
	disabled bool
}

// slotCacheTimeout bounds one save/restore round-trip. Slot files are
// O(context) big (hundreds of MB for long histories), so this is
// generous; it only exists to keep shutdown from hanging forever.
const slotCacheTimeout = 60 * time.Second

// NewSlotCache returns a SlotCache for baseURL, or nil when slot
// caching should not even be attempted for this host. override is the
// tri-state config knob (slot_cache): nil = auto (local/private hosts
// only, same gate as cache_prompt), true = force on, false = force off.
func NewSlotCache(baseURL string, override *bool) *SlotCache {
	enabled := isLocalBaseURL(baseURL)
	if override != nil {
		enabled = *override
	}
	if !enabled {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	return &SlotCache{
		root: u.Scheme + "://" + u.Host,
		http: &http.Client{Timeout: slotCacheTimeout},
	}
}

// Save persists slot 0's KV state under a filename derived from
// sessionID. Returns the number of tokens saved. On the first failure
// the SlotCache disables itself permanently; later calls are silent
// no-ops returning (0, nil).
func (c *SlotCache) Save(ctx context.Context, sessionID string) (int, error) {
	return c.action(ctx, "save", sessionID)
}

// Restore loads a previously saved slot state into slot 0. Call it
// BEFORE the first request of a resumed session so that request prefills
// against the warm KV instead of re-evaluating the whole history.
// Returns the number of tokens restored. Same silent-off contract as
// Save.
func (c *SlotCache) Restore(ctx context.Context, sessionID string) (int, error) {
	return c.action(ctx, "restore", sessionID)
}

// Erase clears slot 0 on the server (no file involved). Used by tests
// to prove a subsequent Restore really reads from disk rather than
// hitting still-resident KV.
func (c *SlotCache) Erase(ctx context.Context) error {
	_, err := c.action(ctx, "erase", "")
	return err
}

// Disabled reports whether the SlotCache has switched itself off after
// a failed probe (or was never usable). Nil receiver counts as disabled
// so call sites can stay unconditional.
func (c *SlotCache) Disabled() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disabled
}

func (c *SlotCache) action(ctx context.Context, action, sessionID string) (int, error) {
	if c == nil {
		return 0, nil
	}
	c.mu.Lock()
	if c.disabled {
		c.mu.Unlock()
		return 0, nil
	}
	c.mu.Unlock()

	var body []byte
	if action != "erase" {
		body, _ = json.Marshal(map[string]string{"filename": SlotFilename(sessionID)})
	} else {
		body = []byte("{}")
	}
	endpoint := c.root + "/slots/0?action=" + action
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, c.disable(fmt.Errorf("slotcache: build %s request: %w", action, err))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, c.disable(fmt.Errorf("slotcache: %s: %w", action, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// 501 = server without --slot-save-path; 404 = pre-slots
		// llama.cpp or a different backend; anything else = broken
		// enough to stop trying.
		return 0, c.disable(fmt.Errorf("slotcache: %s: HTTP %d", action, resp.StatusCode))
	}
	var out struct {
		NSaved    int `json:"n_saved"`
		NRestored int `json:"n_restored"`
		NErased   int `json:"n_erased"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		// 2xx with an unreadable body: the operation itself
		// succeeded server-side; don't disable, just report 0.
		return 0, nil
	}
	switch action {
	case "save":
		return out.NSaved, nil
	case "restore":
		return out.NRestored, nil
	default:
		return out.NErased, nil
	}
}

// disable flips the permanent off switch and passes the error through
// (so the FIRST failure is loggable; later calls are silent no-ops).
func (c *SlotCache) disable(err error) error {
	c.mu.Lock()
	c.disabled = true
	c.mu.Unlock()
	return err
}

// SlotFilename maps a SuperCli session id to the on-server slot file
// name. llama.cpp validates filenames with fs_validate_filename (no
// path separators, no ".."), so everything outside [A-Za-z0-9._-] is
// replaced with '-'.
func SlotFilename(sessionID string) string {
	var b strings.Builder
	for _, r := range sessionID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return "supercli-" + b.String() + ".bin"
}
