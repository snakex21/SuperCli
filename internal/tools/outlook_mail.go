package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// OutlookMail talks to the locally installed desktop Outlook via
// COM automation (Outlook.Application / MAPI namespace). It works
// with whatever mailbox is configured in Outlook. Windows-only;
// on other platforms every call returns an explanatory error.
//
// Read-mostly by design: the ONLY write operation is creating a
// DRAFT (MailItem.Save). Sending, deleting, and moving mail are
// intentionally not implemented.
//
// Registered opt-in (NOT MarkAlwaysOn); discovered via tool_search.
type OutlookMail struct{}

// NewOutlookMail builds the tool.
func NewOutlookMail() *OutlookMail { return &OutlookMail{} }

type outlookArgs struct {
	Op      string `json:"op"`
	Folder  string `json:"folder"`
	Count   int    `json:"count"`
	EntryID string `json:"entry_id"`
	Index   int    `json:"index"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Since   string `json:"since"` // YYYY-MM-DD
	Until   string `json:"until"` // YYYY-MM-DD
	To      string `json:"to"`
	CC      string `json:"cc"`
	Body    string `json:"body"`
}

// Spec returns the tool registration.
func (t *OutlookMail) Spec() Tool {
	return Tool{
		Name: "outlook_mail",
		Description: "Read mail from the local desktop Outlook (Windows COM automation) and create draft emails. " +
			"Operations: 'folders' lists mail folders; 'list' shows the N newest messages in a folder (subject, sender, date, preview); " +
			"'read' returns the full body of one message (by entry_id from list, or by index); " +
			"'search' filters by sender, subject substring, and/or date range; " +
			"'draft' creates a DRAFT message in Outlook (never sends). " +
			"There is no send/delete/move — drafts must be reviewed and sent by the user in Outlook.",
		Schema: `{
  "type": "object",
  "properties": {
    "op":       {"type": "string", "enum": ["folders", "list", "read", "search", "draft"], "description": "Operation to perform."},
    "folder":   {"type": "string", "description": "Folder name or path like 'Inbox' or 'account@example.com/Inbox'. Default: Inbox."},
    "count":    {"type": "integer", "description": "list/search: max messages to return (default 10, max 50)."},
    "entry_id": {"type": "string", "description": "read: Outlook EntryID of the message (returned by list/search)."},
    "index":    {"type": "integer", "description": "read: 1-based position in the folder, newest first (alternative to entry_id)."},
    "from":     {"type": "string", "description": "search: substring matched against sender name or address."},
    "subject":  {"type": "string", "description": "search: substring matched against the subject."},
    "since":    {"type": "string", "description": "search: only messages received on/after this date (YYYY-MM-DD)."},
    "until":    {"type": "string", "description": "search: only messages received on/before this date (YYYY-MM-DD)."},
    "to":       {"type": "string", "description": "draft: recipient address(es), semicolon-separated."},
    "cc":       {"type": "string", "description": "draft: CC address(es), semicolon-separated."},
    "body":     {"type": "string", "description": "draft: plain-text body."}
  },
  "required": ["op"]
}`,
		Fn: t.execute,
	}
}

func (t *OutlookMail) execute(ctx context.Context, args json.RawMessage) (Result, error) {
	var a outlookArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return Result{Err: fmt.Errorf("outlook_mail: bad args: %w", err)}, nil
	}
	a.Op = strings.ToLower(strings.TrimSpace(a.Op))
	if a.Count <= 0 {
		a.Count = 10
	}
	if a.Count > 50 {
		a.Count = 50
	}
	out, err := outlookRun(ctx, a)
	if err != nil {
		return Result{Err: fmt.Errorf("outlook_mail: %w", err)}, nil
	}
	return Result{Text: out}, nil
}
