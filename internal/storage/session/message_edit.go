package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"supercli/internal/llm"
)

// ErrNotAssistantMessage reports an edit request for a non-assistant row.
var ErrNotAssistantMessage = errors.New("session: message is not an assistant message")

// MessageEdit is the stored edit of one assistant message. Original* are the
// exact row payload before the first edit; Edited* are the displayed version.
type MessageEdit struct {
	MessageID         int64
	Seq               int
	OriginalContent   string
	OriginalPartsJSON string
	EditedContent     string
	EditedPartsJSON   string
	ModelVisible      bool
}

// MessageEditResult describes the state after an edit or restore.
type MessageEditResult struct {
	MessageID    int64  `json:"message_id,string"`
	Seq          int    `json:"seq"`
	Edited       bool   `json:"edited"`
	ModelVisible bool   `json:"model_visible"`
	Summarized   bool   `json:"summarized"`
	Content      string `json:"content"`
}

// EditAssistantMessage replaces the visible text of one exact assistant row
// (reasoning parts and tool calls are kept). With modelSees the edited text
// also replaces the original in the model context: in the messages row and,
// when the row is covered by a saved projection, in the matching projection
// message. A message already summarized away stays display-only and reports
// Summarized. Missing or replaced receipts return sql.ErrNoRows.
func (s *Store) EditAssistantMessage(ctx context.Context, sessionID string, receipt MessageReceipt, text string, modelSees bool) (MessageEditResult, error) {
	if strings.TrimSpace(text) == "" {
		return MessageEditResult{}, fmt.Errorf("session.Store.EditAssistantMessage: edited text is empty")
	}
	return s.changeAssistantMessage(ctx, sessionID, receipt, text, false, modelSees)
}

// RestoreAssistantMessage puts back the exact original payload of an edited
// assistant row, in the transcript and (if it was changed) the model context.
func (s *Store) RestoreAssistantMessage(ctx context.Context, sessionID string, receipt MessageReceipt) (MessageEditResult, error) {
	return s.changeAssistantMessage(ctx, sessionID, receipt, "", true, false)
}

func (s *Store) changeAssistantMessage(ctx context.Context, sessionID string, receipt MessageReceipt, text string, restore, modelSees bool) (MessageEditResult, error) {
	if s == nil || s.db == nil {
		return MessageEditResult{}, fmt.Errorf("session.Store: nil store")
	}
	if strings.TrimSpace(sessionID) == "" || receipt.Seq <= 0 || receipt.ID <= 0 {
		return MessageEditResult{}, sql.ErrNoRows
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MessageEditResult{}, err
	}
	defer tx.Rollback()
	// Reserve the writer before reading identity, exactly like truncateFrom.
	reservation, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at = updated_at WHERE id = ?`, sessionID)
	if err != nil {
		return MessageEditResult{}, err
	}
	if n, err := reservation.RowsAffected(); err != nil {
		return MessageEditResult{}, err
	} else if n == 0 {
		return MessageEditResult{}, sql.ErrNoRows
	}
	var id int64
	row := Encoded{SessionID: sessionID, Seq: receipt.Seq}
	err = tx.QueryRowContext(ctx, `SELECT id, role, IFNULL(content,''), IFNULL(parts_json,''), IFNULL(tool_call_id,''), IFNULL(tool_calls_json,''), IFNULL(name,''), IFNULL(origin_json,'') FROM messages WHERE session_id = ? AND seq = ?`,
		sessionID, receipt.Seq).Scan(&id, &row.Role, &row.Content, &row.PartsJSON, &row.ToolCallID, &row.ToolCallsJSON, &row.Name, &row.OriginJSON)
	if err != nil {
		return MessageEditResult{}, err
	}
	if id != receipt.ID {
		return MessageEditResult{}, sql.ErrNoRows
	}
	if row.Role != string(llm.RoleAssistant) {
		return MessageEditResult{}, ErrNotAssistantMessage
	}
	existing, hasEdit, err := readMessageEditTx(ctx, tx, id)
	if err != nil {
		return MessageEditResult{}, err
	}
	original := row
	if hasEdit {
		original.Content, original.PartsJSON = existing.OriginalContent, existing.OriginalPartsJSON
	}
	// The messages row is always the model's version of this message.
	current := row
	result := MessageEditResult{MessageID: id, Seq: receipt.Seq}
	setRow := func(target Encoded) error {
		if target.Content == current.Content && target.PartsJSON == current.PartsJSON {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE messages SET content = ?, parts_json = ? WHERE id = ?`, target.Content, target.PartsJSON, id)
		return err
	}
	revertModelView := func() error {
		if !hasEdit || !existing.ModelVisible {
			return nil
		}
		if _, err := patchProjection(ctx, tx, sessionID, receipt.Seq, current, original); err != nil {
			return err
		}
		return setRow(original)
	}
	if restore {
		if err := revertModelView(); err != nil {
			return MessageEditResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_edits WHERE message_id = ?`, id); err != nil {
			return MessageEditResult{}, err
		}
		result.Content = encodedText(original)
		if err := tx.Commit(); err != nil {
			return MessageEditResult{}, err
		}
		return result, nil
	}
	edited, err := replaceEncodedText(original, text)
	if err != nil {
		return MessageEditResult{}, err
	}
	visible := false
	if modelSees {
		inModel, err := patchProjection(ctx, tx, sessionID, receipt.Seq, current, edited)
		if err != nil {
			return MessageEditResult{}, err
		}
		if inModel {
			if err := setRow(edited); err != nil {
				return MessageEditResult{}, err
			}
			visible = true
		} else {
			// Compaction already summarized this message away: the model sees
			// only the summary, so the edit stays display-only.
			if err := setRow(original); err != nil {
				return MessageEditResult{}, err
			}
			result.Summarized = true
		}
	} else if err := revertModelView(); err != nil {
		return MessageEditResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO message_edits(message_id, session_id, original_content, original_parts_json, edited_content, edited_parts_json, model_visible, updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(message_id) DO UPDATE SET
			edited_content=excluded.edited_content,
			edited_parts_json=excluded.edited_parts_json,
			model_visible=excluded.model_visible,
			updated_at=excluded.updated_at`,
		id, sessionID, original.Content, original.PartsJSON, edited.Content, edited.PartsJSON, visible, time.Now().UTC().UnixNano()); err != nil {
		return MessageEditResult{}, fmt.Errorf("save message edit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return MessageEditResult{}, err
	}
	result.Edited, result.ModelVisible, result.Content = true, visible, encodedText(edited)
	return result, nil
}

// ReadMessageEdits returns edits of messages in [fromSeq, toSeq], keyed by
// physical message ID.
func (s *Store) ReadMessageEdits(ctx context.Context, sessionID string, fromSeq, toSeq int) (map[int64]MessageEdit, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.message_id, m.seq, e.original_content, e.original_parts_json, e.edited_content, e.edited_parts_json, e.model_visible
		FROM message_edits e JOIN messages m ON m.id = e.message_id
		WHERE e.session_id = ? AND m.session_id = ? AND m.seq BETWEEN ? AND ?`, sessionID, sessionID, fromSeq, toSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]MessageEdit{}
	for rows.Next() {
		var e MessageEdit
		if err := rows.Scan(&e.MessageID, &e.Seq, &e.OriginalContent, &e.OriginalPartsJSON, &e.EditedContent, &e.EditedPartsJSON, &e.ModelVisible); err != nil {
			return nil, err
		}
		out[e.MessageID] = e
	}
	return out, rows.Err()
}

// EditedText is the displayed text of the edited version.
func (e MessageEdit) EditedText() string {
	return encodedText(Encoded{Role: string(llm.RoleAssistant), Content: e.EditedContent, PartsJSON: e.EditedPartsJSON})
}

func readMessageEditTx(ctx context.Context, tx *sql.Tx, id int64) (MessageEdit, bool, error) {
	e := MessageEdit{MessageID: id}
	err := tx.QueryRowContext(ctx, `SELECT original_content, original_parts_json, edited_content, edited_parts_json, model_visible FROM message_edits WHERE message_id = ?`, id).
		Scan(&e.OriginalContent, &e.OriginalPartsJSON, &e.EditedContent, &e.EditedPartsJSON, &e.ModelVisible)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageEdit{}, false, nil
	}
	return e, err == nil, err
}

// patchProjection makes the model context show to instead of from. It
// reports whether the message is part of the model context at all: rows
// after the projection boundary (or without a projection) are read directly
// from messages; covered rows must be found in the saved snapshot by their
// current text. Identical replies are disambiguated by counting the later
// covered assistant rows with the same text, matched from the end.
func patchProjection(ctx context.Context, tx *sql.Tx, sessionID string, seq int, from, to Encoded) (bool, error) {
	var through int
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT through_seq, messages_json FROM session_context_projections WHERE session_id = ?`, sessionID).Scan(&through, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var msgs []llm.Message
	if json.Unmarshal(raw, &msgs) != nil || !validMessages(msgs) {
		// ReadModelContext ignores a corrupt projection and reads all rows.
		return true, nil
	}
	if seq > through {
		return true, nil
	}
	fromText := encodedText(from)
	if fromText == "" {
		return false, nil
	}
	later := 0
	rows, err := tx.QueryContext(ctx, `SELECT IFNULL(content,''), IFNULL(parts_json,'') FROM messages WHERE session_id = ? AND role = ? AND seq > ? AND seq <= ?`,
		sessionID, string(llm.RoleAssistant), seq, through)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		e := Encoded{Role: string(llm.RoleAssistant)}
		if err := rows.Scan(&e.Content, &e.PartsJSON); err != nil {
			rows.Close()
			return false, err
		}
		if encodedText(e) == fromText {
			later++
		}
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	toText := encodedText(to)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != llm.RoleAssistant || msgs[i].TextOnly().Content != fromText {
			continue
		}
		if later > 0 {
			later--
			continue
		}
		msgs[i] = replaceMessageText(msgs[i], toText)
		out, err := json.Marshal(msgs)
		if err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE session_context_projections SET messages_json = ? WHERE session_id = ?`, out, sessionID); err != nil {
			return false, fmt.Errorf("projection edit: %w", err)
		}
		return true, nil
	}
	return false, nil
}

// encodedText is the visible text of a row, as shown in the transcript.
func encodedText(e Encoded) string {
	m, err := e.ToMessage()
	if err != nil {
		return e.Content
	}
	return m.TextOnly().Content
}

// replaceEncodedText changes only content/parts_json; every other column
// keeps its exact stored bytes.
func replaceEncodedText(e Encoded, text string) (Encoded, error) {
	m, err := e.ToMessage()
	if err != nil {
		return Encoded{}, err
	}
	enc, err := FromMessage(replaceMessageText(m, text))
	if err != nil {
		return Encoded{}, err
	}
	out := e
	out.Content, out.PartsJSON = enc.Content, enc.PartsJSON
	return out, nil
}

// replaceMessageText puts text into the first text part (dropping further
// text parts) or into Content; reasoning/image parts and tool calls remain.
func replaceMessageText(m llm.Message, text string) llm.Message {
	var parts []llm.ContentPart
	placed := false
	for _, p := range m.Parts {
		if p.Type == llm.PartTypeText {
			if placed {
				continue
			}
			p.Text, placed = text, true
		}
		parts = append(parts, p)
	}
	m.Parts = parts
	if placed {
		m.Content = ""
	} else {
		m.Content = text
	}
	return m
}
