package mail

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
// Read-mostly by design. Write operations are deliberately narrow:
// creating a DRAFT, moving explicitly filtered messages to Outlook's
// default Deleted Items/Trash folder, and permanently deleting explicitly
// filtered messages that are already in that default Trash folder. Sending is
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
	Path    string `json:"path"`
	Index   int    `json:"index"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Since   string `json:"since"` // YYYY-MM-DD
	Until   string `json:"until"` // YYYY-MM-DD
	To      string `json:"to"`
	CC      string `json:"cc"`
	Body    string `json:"body"`
	Confirm bool   `json:"confirm"`
	All     bool   `json:"all"`
	Scope   string `json:"scope"`
}

// Spec returns the tool registration.
func (t *OutlookMail) Spec() Tool {
	return Tool{
		Name: "outlook_mail",
		Description: "Use the locally installed desktop Outlook through Windows COM automation. " +
			"Operations: 'folders' lists mail folders; 'list' shows the N newest messages in a folder; " +
			"'read' returns one full message; 'search' filters by sender, subject and/or date; 'count' returns only the number of matching messages without listing them; 'sync_status' reports recent Outlook synchronization errors; 'export_msg' saves one exact message identified by entry_id as a native Outlook .msg file without deleting it. " +
			"For search/count, scope:'account' searches the entire default Outlook account recursively (including Gmail subfolders) instead of only one folder; search also supports scope:'all_stores' to find matching mail across every Outlook store/PST without listing unrelated messages. " +
			"'draft' creates a DRAFT and never sends; 'trash' moves matching messages from a folder to Outlook's default Deleted Items/Trash folder; " +
			"'purge' permanently deletes matching messages that are already in Outlook's default Deleted Items/Trash folder. " +
			"For trash/purge, at least one filter (from/subject/since/until) is required and confirm:true is mandatory; only use confirm:true after the user explicitly approved that exact cleanup. " +
			"Purge cannot be undone. There is no send operation.",
		Schema: `{
  "type": "object",
  "properties": {
    "op":       {"type": "string", "enum": ["folders", "list", "read", "search", "count", "sync_status", "export_msg", "draft", "trash", "purge"], "description": "Operation to perform."},
    "folder":   {"type": "string", "description": "Folder name or path like 'Inbox' or 'account@example.com/Inbox'. Default: Inbox."},
    "count":    {"type": "integer", "description": "list/search: max messages to return (default 10, max 50)."},
    "entry_id": {"type": "string", "description": "read/export_msg: Outlook EntryID of the exact message (returned by list/search)."},
    "path":     {"type": "string", "description": "export_msg only: target .msg file path. Existing files are not overwritten."},
    "index":    {"type": "integer", "description": "read: 1-based position in the folder, newest first (alternative to entry_id)."},
    "from":     {"type": "string", "description": "search: substring matched against sender name or address."},
    "subject":  {"type": "string", "description": "search: substring matched against the subject."},
    "since":    {"type": "string", "description": "search: only messages received on/after this date (YYYY-MM-DD)."},
    "until":    {"type": "string", "description": "search: only messages received on/before this date (YYYY-MM-DD)."},
    "to":       {"type": "string", "description": "draft: recipient address(es), semicolon-separated."},
    "cc":       {"type": "string", "description": "draft: CC address(es), semicolon-separated."},
    "body":     {"type": "string", "description": "draft: plain-text body."},
    "confirm":  {"type": "boolean", "description": "trash/purge only: must be true after explicit user approval. purge is permanent."},
    "all":      {"type": "boolean", "description": "trash/purge only: process all matching messages in one call after explicit approval. Prefer this for bulk cleanup to avoid repeated tool calls."},
    "scope":    {"type": "string", "enum": ["folder", "account", "all_stores"], "description": "search/count. 'folder' (default) searches the selected folder; 'account' searches all mail folders in the default Outlook account recursively and deduplicates messages; 'all_stores' is supported by search and scans all Outlook stores/PSTs while returning only matching mail."}
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
	a.Scope = strings.ToLower(strings.TrimSpace(a.Scope))
	if a.Scope == "" {
		a.Scope = "folder"
	}
	if a.Op == "trash" || a.Op == "purge" {
		if a.All {
			a.Count = 10000
		} else {
			if a.Count <= 0 {
				a.Count = 100
			}
			if a.Count > 500 {
				a.Count = 500
			}
		}
	} else {
		if a.Count <= 0 {
			a.Count = 10
		}
		if a.Count > 50 {
			a.Count = 50
		}
	}
	out, err := outlookRun(ctx, a)
	if err != nil {
		return Result{Err: fmt.Errorf("outlook_mail: %w", err)}, nil
	}
	return Result{Text: out}, nil
}
