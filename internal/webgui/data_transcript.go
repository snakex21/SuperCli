package webgui

import (
	"context"
	"strconv"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/tools/core"
)

func (e *Engine) transcript(ctx context.Context, id string) ([]transcriptMsg, error) {
	store, err := e.sessionStore()
	if err != nil {
		return []transcriptMsg{}, nil
	}
	meta, err := store.Get(id)
	if err != nil {
		return nil, err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return nil, errSessionOutsideWorkspace
	}
	rows, err := store.ReadTranscriptMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	return e.buildTranscript(ctx, store, id, rows)
}

func (e *Engine) transcriptPage(ctx context.Context, id string, beforeSeq, limit int) (transcriptPage, error) {
	store, err := e.sessionStore()
	if err != nil {
		return transcriptPage{Messages: []transcriptMsg{}}, nil
	}
	meta, err := store.Get(id)
	if err != nil {
		return transcriptPage{}, err
	}
	if !sameSessionWorkspace(meta.Cwd, e.Home()) {
		return transcriptPage{}, errSessionOutsideWorkspace
	}
	rows, hasMore, err := store.ReadTranscriptMessagesBefore(ctx, id, beforeSeq, limit)
	if err != nil {
		return transcriptPage{}, err
	}
	messages, err := e.buildTranscript(ctx, store, id, rows)
	if err != nil {
		return transcriptPage{}, err
	}
	cursor := 0
	if len(rows) > 0 {
		// Pages containing only legacy summaries still need an advancing cursor.
		cursor = rows[0].Seq
	}
	return transcriptPage{Messages: messages, HasMore: hasMore, BeforeSeq: cursor}, nil
}

func (e *Engine) buildTranscript(ctx context.Context, store *session.Store, id string, rows []session.TranscriptMessage) ([]transcriptMsg, error) {
	if len(rows) == 0 {
		return []transcriptMsg{}, nil
	}
	fromSeq, toSeq := 0, 0
	fromSeq, toSeq = rows[0].Seq, rows[len(rows)-1].Seq
	turnRows, err := store.ReadTurnSummariesRange(ctx, id, fromSeq, toSeq)
	if err != nil {
		return nil, err
	}
	e.recoverCheckpointChanges(ctx, store, id, turnRows)
	turns := make(map[int]session.TurnSummary, len(turnRows))
	for _, turn := range turnRows {
		turns[turn.AssistantSeq] = turn
	}
	attachments, err := store.ReadMessageAttachmentsRange(ctx, id, fromSeq, toSeq)
	if err != nil {
		return nil, err
	}
	edits, err := store.ReadMessageEdits(ctx, id, fromSeq, toSeq)
	if err != nil {
		return nil, err
	}
	out := make([]transcriptMsg, 0, len(rows))
	var imageContext []llm.Message
	loadedImageContext := false
	for _, m := range rows {
		msg, err := m.ToMessage()
		if err != nil {
			return nil, err
		}
		if agent.IsLegacyCompactionSummary(msg) {
			continue
		}
		presentation := msg
		if legacyToolImageName(msg, len(attachments[m.Seq]) != 0) != "" {
			// A page may start between a tool result and its old image carrier.
			// Read a bounded prefix only for that specific missing context.
			if !loadedImageContext && legacyToolImageNeedsContext(imageContext) {
				prefix, _, readErr := store.ReadTranscriptMessagesBefore(ctx, id, rows[0].Seq, 64)
				if readErr != nil {
					return nil, readErr
				}
				var older []llm.Message
				for _, row := range prefix {
					value, decodeErr := row.ToMessage()
					if decodeErr != nil {
						return nil, decodeErr
					}
					older = append(older, value)
				}
				imageContext = append(older, imageContext...)
				loadedImageContext = true
			}
			presentation = projectLegacyToolImage(msg, imageContext)
		}
		imageContext = append(imageContext, msg)
		if len(imageContext) > 64 {
			imageContext = imageContext[len(imageContext)-64:]
		}
		textOnly := msg.TextOnly()
		item := transcriptMsg{
			WorkerActivity: transcriptWorkerActivity(msg),
			Seq:            m.Seq,
			Role:           m.Role,
			Content:        textOnly.Content,
			Attachments:    append([]string(nil), attachments[m.Seq]...),
			Name:           m.Name,
			ToolCallID:     msg.ToolCallID,
		}
		if msg.Role == llm.RoleTool || isTranscriptWorkerNotification(msg) {
			item.ToolOutputHandle = core.StoredOutputHandle(msg.Content)
		}
		if _, ok := legacyWorkerObservations(msg); ok && len(attachments[m.Seq]) == 0 && m.MessageID > 0 {
			item.WorkerObservationsID = strconv.FormatInt(m.MessageID, 10)
		}
		if len(item.Attachments) == 0 && isTranscriptWorkerNotification(msg) {
			// Delivery to the model used RoleUser; presentation must use the
			// existing folded worker card, never a human bubble or rewind point.
			item.Role, item.Name = string(llm.RoleTool), "task"
		}
		item.ToolPreviews = transcriptToolPreviews(id, msg)
		item.ToolImages = transcriptToolImagePreviews(id, presentation, len(item.Attachments) != 0)
		item.ToolImageCarrier = isTranscriptToolImageCarrier(presentation, len(item.Attachments) != 0)
		if item.Role == string(llm.RoleUser) && m.MessageID > 0 && !item.ToolImageCarrier {
			item.MessageID = strconv.FormatInt(m.MessageID, 10)
		}
		if item.Role == string(llm.RoleAssistant) && m.MessageID > 0 {
			item.MessageID = strconv.FormatInt(m.MessageID, 10)
			if edit, ok := edits[m.MessageID]; ok {
				item.Content, item.Edited, item.EditModelVisible = edit.EditedText(), true, edit.ModelVisible
			}
		}
		if !item.ToolImageCarrier && len(item.Attachments) == 0 {
			for _, part := range msg.Parts {
				if part.Type == llm.PartTypeImage && part.Image != nil {
					if token := sessionImagePreviewPath(id, part.Image.Path); token != "" {
						item.Attachments = append(item.Attachments, token)
					}
				}
			}
		}
		for _, call := range msg.ToolCalls {
			item.ToolCalls = append(item.ToolCalls, transcriptToolCall{
				ID: call.ID, Name: call.Name, Arguments: call.Arguments,
			})
		}
		if turn, ok := turns[m.Seq]; ok {
			item.Turn = &transcriptTurn{
				ElapsedMS: turn.DurationMS, TokIn: turn.Input, TokOut: turn.Output,
				TokTotal: turn.Input + turn.Output, TokCached: turn.CachedInput,
				ReasoningTok: turn.Reasoning, ToolCalls: turn.ToolCalls,
				FileChanges: append([]session.FileChange(nil), turn.FileChanges...),
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// transcriptToolPreviews returns portable preview handles for user-only tool
// preview images recorded on a host-authored tool result. Only content-
// addressed session-media files are exposed.
func transcriptToolPreviews(sessionID string, msg llm.Message) []string {
	if msg.Role != llm.RoleTool || msg.Origin == nil || msg.Origin.Source != llm.SourceToolResult {
		return nil
	}
	var out []string
	for _, path := range msg.Origin.ToolPreviews {
		if token := sessionImagePreviewPath(sessionID, path); token != "" {
			out = append(out, token)
		}
		if len(out) >= 16 {
			break
		}
	}
	return out
}

// Only host-authored image carriers are reparented. The transcript projection
// may first recover provenance for a strictly matched legacy host envelope;
// ordinary user messages and actual attachment records remain unchanged.
func transcriptToolImagePreviews(sessionID string, msg llm.Message, realAttachments bool) []transcriptToolImage {
	if realAttachments || msg.Role != llm.RoleUser || msg.Content != "" || msg.Name != "" || msg.ToolCallID != "" || len(msg.ToolCalls) != 0 {
		return nil
	}
	var images []transcriptToolImage
	for _, part := range msg.Parts {
		switch part.Type {
		case llm.PartTypeText:
			// Surrounding host text is covered by the explicit carrier marker,
			// never recognized by its words or an image name.
		case llm.PartTypeImage:
			if part.Image == nil || !part.Image.ToolOutputCarrier || part.Image.SourceToolCallID == "" {
				return nil
			}
			token := sessionImagePreviewPath(sessionID, part.Image.Path)
			if token == "" {
				return nil
			}
			images = append(images, transcriptToolImage{SourceCallID: part.Image.SourceToolCallID, Path: token})
		default:
			return nil
		}
	}
	return images
}

// memoryList returns recent memory entries across both scopes
// (project + global). A scope filter of "" returns everything.
