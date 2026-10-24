package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"supercli/internal/llm"
)

// Portable single-session files move ONE conversation between SuperCli
// installs. The file is a single JSON document; messages are carried exactly
// as stored (column-shaped Encoded payload), so the model-visible history —
// including tool calls, tool results, reasoning parts, context origins and
// inline images — round-trips unchanged. Edits of assistant replies are
// carried with their original payload so "restore original" keeps working.
//
// Not carried: file-backed images (ImageRef.Path into the source install's
// session-media folder; the reference is kept but the pixels are not
// embedded), attachment path lists, usage/billing journal rows, context
// projections/summaries, checkpoints, queue entries and branch parent links.
const (
	PortableFormat        = "supercli-session"
	PortableVersion       = 1
	PortableFileSuffix    = ".supercli-session.json"
	MaxPortableBytes      = 64 << 20
	maxPortableMessages   = 200000
	maxPortableFieldBytes = 32 << 20
)

// ErrPortableTooLarge reports input above MaxPortableBytes.
var ErrPortableTooLarge = errors.New("session file is larger than the allowed limit")

// ErrPortableInvalid wraps every malformed-input rejection.
var ErrPortableInvalid = errors.New("invalid SuperCli session file")

// PortableSession is the versioned on-disk document.
type PortableSession struct {
	Format     string            `json:"format"`
	Version    int               `json:"version"`
	ExportedAt time.Time         `json:"exported_at"`
	Session    PortableMeta      `json:"session"`
	Messages   []PortableMessage `json:"messages"`
	Edits      []PortableEdit    `json:"edits,omitempty"`
}

// PortableMeta is the session metadata. ID is the source ID (informational;
// import always assigns a new one).
type PortableMeta struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Model           string    `json:"model"`
	Provider        string    `json:"provider,omitempty"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	Cwd             string    `json:"cwd"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	MessageCount    int       `json:"message_count"`
	TokenIn         int       `json:"token_in"`
	TokenOut        int       `json:"token_out"`
}

// PortableMessage is one stored message row (JSON columns stay strings).
type PortableMessage struct {
	Seq           int    `json:"seq"`
	Role          string `json:"role"`
	Content       string `json:"content"`
	PartsJSON     string `json:"parts_json,omitempty"`
	ToolCallID    string `json:"tool_call_id,omitempty"`
	ToolCallsJSON string `json:"tool_calls_json,omitempty"`
	Name          string `json:"name,omitempty"`
	OriginJSON    string `json:"origin_json,omitempty"`
}

// PortableEdit is one assistant-message edit, addressed by message seq.
type PortableEdit struct {
	Seq               int    `json:"seq"`
	OriginalContent   string `json:"original_content"`
	OriginalPartsJSON string `json:"original_parts_json,omitempty"`
	EditedContent     string `json:"edited_content"`
	EditedPartsJSON   string `json:"edited_parts_json,omitempty"`
	ModelVisible      bool   `json:"model_visible"`
}

// PortableImportOptions controls ImportPortable.
type PortableImportOptions struct {
	// Cwd, when non-empty, replaces the recorded working directory so the
	// imported session is listed for the importing project. The original
	// path from the file is otherwise kept.
	Cwd string
}

// PortableFilename returns a safe default file name for a session.
func PortableFilename(sess Session) string {
	id := sess.ID
	if len(id) > 16 {
		id = id[:16]
	}
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		b.WriteString("session")
	}
	return "session-" + b.String() + PortableFileSuffix
}

// ExportPortable reads one session with messages and edits.
func (s *Store) ExportPortable(ctx context.Context, sessionID string) (PortableSession, error) {
	if s == nil || s.db == nil {
		return PortableSession{}, fmt.Errorf("session.Store.ExportPortable: nil store")
	}
	sess, err := s.Get(strings.TrimSpace(sessionID))
	if err != nil {
		return PortableSession{}, err
	}
	rows, err := s.ReadMessages(ctx, sess.ID)
	if err != nil {
		return PortableSession{}, fmt.Errorf("session.Store.ExportPortable: messages: %w", err)
	}
	out := PortableSession{
		Format:     PortableFormat,
		Version:    PortableVersion,
		ExportedAt: time.Now().UTC(),
		Session: PortableMeta{
			ID: sess.ID, Title: sess.Title, Model: sess.Model, Provider: sess.Provider,
			ReasoningEffort: sess.ReasoningEffort, Cwd: sess.Cwd,
			CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt,
			MessageCount: len(rows), TokenIn: sess.TokenIn, TokenOut: sess.TokenOut,
		},
		Messages: make([]PortableMessage, 0, len(rows)),
	}
	for _, r := range rows {
		out.Messages = append(out.Messages, PortableMessage{
			Seq: r.Seq, Role: r.Role, Content: r.Content, PartsJSON: r.PartsJSON,
			ToolCallID: r.ToolCallID, ToolCallsJSON: r.ToolCallsJSON, Name: r.Name, OriginJSON: r.OriginJSON,
		})
	}
	edits, err := s.ReadMessageEdits(ctx, sess.ID, 1, math.MaxInt32)
	if err != nil {
		return PortableSession{}, fmt.Errorf("session.Store.ExportPortable: edits: %w", err)
	}
	for _, e := range edits {
		out.Edits = append(out.Edits, PortableEdit{
			Seq: e.Seq, OriginalContent: e.OriginalContent, OriginalPartsJSON: e.OriginalPartsJSON,
			EditedContent: e.EditedContent, EditedPartsJSON: e.EditedPartsJSON, ModelVisible: e.ModelVisible,
		})
	}
	sortPortableEdits(out.Edits)
	return out, nil
}

func sortPortableEdits(edits []PortableEdit) {
	for i := 1; i < len(edits); i++ {
		for j := i; j > 0 && edits[j].Seq < edits[j-1].Seq; j-- {
			edits[j], edits[j-1] = edits[j-1], edits[j]
		}
	}
}

// WritePortable exports one session as indented JSON.
func (s *Store) WritePortable(ctx context.Context, sessionID string, w io.Writer) (PortableSession, error) {
	doc, err := s.ExportPortable(ctx, sessionID)
	if err != nil {
		return PortableSession{}, err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	if err := enc.Encode(doc); err != nil {
		return PortableSession{}, err
	}
	return doc, nil
}

// DecodePortable reads and validates a portable session document. Input
// above MaxPortableBytes returns ErrPortableTooLarge; every other rejection
// wraps ErrPortableInvalid.
func DecodePortable(r io.Reader) (PortableSession, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxPortableBytes+1))
	if err != nil {
		return PortableSession{}, err
	}
	if len(data) > MaxPortableBytes {
		return PortableSession{}, ErrPortableTooLarge
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var head struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return PortableSession{}, fmt.Errorf("%w: not JSON: %v", ErrPortableInvalid, err)
	}
	if head.Format != PortableFormat {
		return PortableSession{}, fmt.Errorf("%w: format %q is not %q", ErrPortableInvalid, head.Format, PortableFormat)
	}
	if head.Version != PortableVersion {
		return PortableSession{}, fmt.Errorf("%w: unsupported version %d (supported: %d)", ErrPortableInvalid, head.Version, PortableVersion)
	}
	var doc PortableSession
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return PortableSession{}, fmt.Errorf("%w: %v", ErrPortableInvalid, err)
	}
	if dec.More() {
		return PortableSession{}, fmt.Errorf("%w: trailing data after document", ErrPortableInvalid)
	}
	if err := doc.Validate(); err != nil {
		return PortableSession{}, err
	}
	return doc, nil
}

// Validate checks structure and that every message decodes into a valid
// model message.
func (p PortableSession) Validate() error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrPortableInvalid}, args...)...)
	}
	if p.Format != PortableFormat || p.Version != PortableVersion {
		return bad("format/version mismatch")
	}
	if strings.TrimSpace(p.Session.Model) == "" {
		return bad("session model is empty")
	}
	if len(p.Messages) == 0 {
		return bad("no messages")
	}
	if len(p.Messages) > maxPortableMessages {
		return bad("too many messages (%d)", len(p.Messages))
	}
	roles := make(map[int]string, len(p.Messages))
	last := 0
	for i, m := range p.Messages {
		if m.Seq <= last {
			return bad("message %d: sequence %d is not increasing", i, m.Seq)
		}
		last = m.Seq
		for _, field := range []string{m.Content, m.PartsJSON, m.ToolCallsJSON, m.OriginJSON} {
			if len(field) > maxPortableFieldBytes {
				return bad("message %d: field too large", i)
			}
		}
		if _, err := m.encoded("").ToMessage(); err != nil {
			return bad("message %d: %v", i, err)
		}
		roles[m.Seq] = m.Role
	}
	seen := map[int]bool{}
	for i, e := range p.Edits {
		if roles[e.Seq] != string(llm.RoleAssistant) {
			return bad("edit %d: sequence %d is not an assistant message", i, e.Seq)
		}
		if seen[e.Seq] {
			return bad("edit %d: duplicate edit for sequence %d", i, e.Seq)
		}
		seen[e.Seq] = true
		for _, parts := range []string{e.OriginalPartsJSON, e.EditedPartsJSON} {
			if parts != "" && !json.Valid([]byte(parts)) {
				return bad("edit %d: parts are not JSON", i)
			}
		}
	}
	return nil
}

func (m PortableMessage) encoded(sessionID string) Encoded {
	return Encoded{
		SessionID: sessionID, Seq: m.Seq, Role: m.Role, Content: m.Content, PartsJSON: m.PartsJSON,
		ToolCallID: m.ToolCallID, ToolCallsJSON: m.ToolCallsJSON, Name: m.Name, OriginJSON: m.OriginJSON,
	}
}

// ImportPortable validates doc and stores it as a NEW session (new ID) in
// one transaction. Existing sessions are never touched.
func (s *Store) ImportPortable(ctx context.Context, doc PortableSession, opts PortableImportOptions) (Session, error) {
	if s == nil || s.db == nil {
		return Session{}, fmt.Errorf("session.Store.ImportPortable: nil store")
	}
	if err := doc.Validate(); err != nil {
		return Session{}, err
	}
	meta := doc.Session
	cwd := strings.TrimSpace(opts.Cwd)
	if cwd == "" {
		cwd = strings.TrimSpace(meta.Cwd)
	}
	if cwd == "" {
		return Session{}, fmt.Errorf("%w: session cwd is empty", ErrPortableInvalid)
	}
	now := time.Now().UTC()
	created, updated := meta.CreatedAt.UTC(), meta.UpdatedAt.UTC()
	if created.IsZero() || created.After(now) {
		created = now
	}
	if updated.IsZero() || updated.Before(created) || updated.After(now) {
		updated = now
	}
	tokenIn, tokenOut := max(meta.TokenIn, 0), max(meta.TokenOut, 0)
	id := newID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions(id,cwd,title,model,provider,reasoning_effort,parent_id,created_at,updated_at,message_count,token_in,token_out) VALUES(?,?,?,?,?,?,NULL,?,?,?,?,?)`,
		id, cwd, meta.Title, meta.Model, meta.Provider, meta.ReasoningEffort, created.UnixNano(), updated.UnixNano(), len(doc.Messages), tokenIn, tokenOut,
	); err != nil {
		return Session{}, fmt.Errorf("session.Store.ImportPortable: session: %w", err)
	}
	ids := make(map[int]int64, len(doc.Messages))
	for _, m := range doc.Messages {
		var rowID int64
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO messages(session_id, seq, role, content, parts_json, tool_call_id, tool_calls_json, name, origin_json, created_at) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING id`,
			id, m.Seq, m.Role, m.Content, m.PartsJSON, m.ToolCallID, m.ToolCallsJSON, m.Name, m.OriginJSON, updated.UnixNano(),
		).Scan(&rowID); err != nil {
			return Session{}, fmt.Errorf("session.Store.ImportPortable: message %d: %w", m.Seq, err)
		}
		ids[m.Seq] = rowID
	}
	for _, e := range doc.Edits {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO message_edits(message_id, session_id, original_content, original_parts_json, edited_content, edited_parts_json, model_visible, updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			ids[e.Seq], id, e.OriginalContent, e.OriginalPartsJSON, e.EditedContent, e.EditedPartsJSON, e.ModelVisible, now.UnixNano(),
		); err != nil {
			return Session{}, fmt.Errorf("session.Store.ImportPortable: edit %d: %w", e.Seq, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return Session{
		ID: id, Cwd: cwd, Title: meta.Title, Model: meta.Model, Provider: meta.Provider,
		ReasoningEffort: meta.ReasoningEffort, CreatedAt: created, UpdatedAt: updated,
		MessageCount: len(doc.Messages), TokenIn: tokenIn, TokenOut: tokenOut,
	}, nil
}

// ImportPortableReader decodes and imports in one step.
func (s *Store) ImportPortableReader(ctx context.Context, r io.Reader, opts PortableImportOptions) (Session, error) {
	doc, err := DecodePortable(r)
	if err != nil {
		return Session{}, err
	}
	return s.ImportPortable(ctx, doc, opts)
}
