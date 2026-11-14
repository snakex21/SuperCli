package mail

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	thunderbirdBridgeAddr  = "127.0.0.1:47831"
	thunderbirdBridgeToken = "supercli-thunderbird-local-v1-7b2e44c91a"
)

// ThunderbirdMail talks to a tiny local MailExtension running inside
// Thunderbird. Thunderbird itself owns the authenticated IMAP/OAuth session;
// SuperCLI never reads passwords or OAuth tokens. The first version is
// intentionally read-only so it can be validated against Gmail before any
// mutation support is enabled.
type ThunderbirdMail struct {
	dataDir string
	// previewCall replaces the bridge for approval previews in tests. The
	// preview only issues read-only operations (accounts, count, search,
	// attachments).
	previewCall func(ctx context.Context, op string, raw json.RawMessage) (json.RawMessage, error)
}

// NewThunderbirdMail accepts the resolved app data directory. Omitting it keeps
// the portable executable-relative default and SUPERCLI_DATA_DIR override.
func NewThunderbirdMail(dataDir ...string) *ThunderbirdMail {
	t := &ThunderbirdMail{}
	if len(dataDir) > 0 {
		t.dataDir = strings.TrimSpace(dataDir[0])
	}
	return t
}

type thunderbirdContactInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type thunderbirdToolArgs struct {
	Op                  string                    `json:"op"`
	Account             string                    `json:"account"`
	Folder              string                    `json:"folder"`
	Destination         string                    `json:"destination"`
	DestinationAccount  string                    `json:"destination_account"`
	Path                string                    `json:"path"`
	MessageID           int64                     `json:"message_id"`
	MessageIDs          []int64                   `json:"message_ids"`
	MaxChars            int                       `json:"max_chars"`
	StartChar           int                       `json:"start_char"`
	PartName            string                    `json:"part_name"`
	AttachmentName      string                    `json:"attachment_name"`
	From                string                    `json:"from"`
	To                  string                    `json:"to"`
	Email               string                    `json:"email"`
	Address             string                    `json:"address"`
	Subject             string                    `json:"subject"`
	HeaderMessageID     string                    `json:"header_message_id"`
	HeaderMessageIDs    []string                  `json:"header_message_ids"`
	AddressBook         string                    `json:"address_book"`
	ContactID           string                    `json:"contact_id"`
	Contacts            []thunderbirdContactInput `json:"contacts"`
	LocalBackupVerified bool                      `json:"local_backup_verified"`
	Text                string                    `json:"text"`
	Name                string                    `json:"name"`
	NewName             string                    `json:"new_name"`
	Parent              string                    `json:"parent"`
	Since               string                    `json:"since"`
	Until               string                    `json:"until"`
	Confirm             bool                      `json:"confirm"`
	All                 bool                      `json:"all"`
	BatchSize           int                       `json:"batch_size"`
	Continuation        string                    `json:"continuation"`
}

func thunderbirdHasMutationSelector(args thunderbirdToolArgs) bool {
	return len(args.MessageIDs) > 0 ||
		strings.TrimSpace(args.HeaderMessageID) != "" ||
		len(args.HeaderMessageIDs) > 0 ||
		strings.TrimSpace(args.From) != "" ||
		strings.TrimSpace(args.To) != "" ||
		strings.TrimSpace(args.Address) != "" ||
		strings.TrimSpace(args.Subject) != "" ||
		strings.TrimSpace(args.Text) != "" ||
		strings.TrimSpace(args.Since) != "" ||
		strings.TrimSpace(args.Until) != ""
}

func validateThunderbirdMoveArgs(args thunderbirdToolArgs) error {
	if !args.Confirm {
		return fmt.Errorf("thunderbird_mail: move requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt")
	}
	if strings.TrimSpace(args.Destination) == "" {
		return fmt.Errorf("thunderbird_mail: move requires destination")
	}
	if !args.All && !thunderbirdHasMutationSelector(args) {
		return fmt.Errorf("thunderbird_mail: move requires at least one filter or exact identifier (from/to/address/subject/text/since/until/message_ids/header_message_id/header_message_ids) or all:true for an explicitly approved whole-folder move")
	}
	return nil
}

type thunderbirdBridgeRequest struct {
	ID   string          `json:"id"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args"`
}

type thunderbirdBridgeResponse struct {
	ID    string          `json:"id"`
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
	// NotExecuted marks an explicit refusal before execution (for example a
	// full extension result cache): safe to retry later with a new request.
	NotExecuted bool `json:"notExecuted,omitempty"`
}

type thunderbirdBridgeState struct {
	once               sync.Once
	startErr           error
	dataDir            string // guarded by mu; pinned on first operation
	queue              chan thunderbirdBridgeRequest
	mu                 sync.Mutex
	waiters            map[string]chan thunderbirdBridgeResponse
	completed          map[string]thunderbirdCompletion
	completionSequence uint64 // guarded by mu; breaks ties on coarse clocks
	dispatched         map[string]bool
	transfers          map[string]thunderbirdFileTransfer
	downloads          map[string]thunderbirdDownloadedAttachment
	lastPoll           time.Time
	activePolls        int
	requestID          atomic.Uint64
}

var globalThunderbirdBridge = &thunderbirdBridgeState{
	queue:     make(chan thunderbirdBridgeRequest, 32),
	waiters:   make(map[string]chan thunderbirdBridgeResponse),
	transfers: make(map[string]thunderbirdFileTransfer),
	downloads: make(map[string]thunderbirdDownloadedAttachment),
}

func (t *ThunderbirdMail) Spec() Tool {
	return Tool{
		Name: "thunderbird_mail",
		Description: "Use the locally running Thunderbird through the SuperCLI Thunderbird Bridge extension. " +
			"Thunderbird owns the real Gmail IMAP/OAuth connection, so prefer this over Outlook COM for Gmail mailbox state. " +
			"Read operations: 'status', 'accounts', 'folders', 'count', 'search', 'read', 'attachments', 'get_attachment', 'senders', 'largest', 'address_books', 'contacts', and 'contact_candidates'. search/count can filter by sender (from), recipient (to, covers To and Cc), sender-or-recipient email address (address), subject substring, text and date. from/to/address also accept a domain: '@example.com' matches mailboxes at example.com or its subdomains only, a bare 'example.com' additionally matches display names; domain filters scan every header of the folder, so keep the Inbox scope or add dates. For a company or brand the user names (allegro, discord, github), filter with from:'<brand>': a bare word matches sender display names and addresses ('allegro' finds 'Poczta Allegro' and allegromail.pl, while 'allegro.pl' misses allegromail.pl). Gmail filters often file such mail outside Inbox, so when Inbox returns 0 repeat with scope:'account' (or the matching folder) before saying there is none. 'text' is full-text over bodies and slow, especially with scope:'account'; use it only when from/subject/to cannot express the request. There is NO unread/read filter: for 'how many unread' use op 'folders' and report unreadMessageCount (Inbox/Odebrane for the inbox; Gmail All Mail/Wszystkie for the whole account without Trash/Spam). Unknown arguments are rejected without executing anything. count/search/senders/largest count each RFC Message-ID once (Gmail labels, All Mail and duplicate index records are merged; see deduplicatedByMessageId, duplicatesSkipped, duplicateRecords, crossFolderDuplicates), so totals can be lower than Thunderbird's raw counts; pass include_duplicates:true only when raw per-record results are needed. search results are newest-first. 'contact_candidates' aggregates sender and recipient addresses from message headers without loading bodies, excluding the mailbox owner's own address. 'largest' scans message headers locally and returns only the largest 1-100 messages with byte sizes and stable Message-ID headers, avoiding large AI context. Use 'read' with a message_id returned by search to retrieve the decoded body without changing read/unread state. read returns at most max_chars (default 12000); when hasMore:true, repeat with nextStartChar as start_char instead of loading a huge email into a small local model at once. search does NOT return attachment metadata by default (attachmentsIncluded:false, no hasAttachments/attachmentCount/attachments fields) because it costs one MIME parse per result; pass include_attachments:true when attachments matter, or call 'attachments' (full filename including extension, extension, MIME type, size, partName) or 'read' for one message_id. 'get_attachment' fetches the actual attachment bytes. For PNG/JPEG/GIF/WebP it returns a real image to the agent loop so a vision-capable model receives the pixels on the next model turn; for PDF/Office/other files it returns a temporary localPath so the appropriate document tool can read it. count/search/senders/largest default to Inbox to avoid Gmail label duplicates; use scope:'account' only when an explicit whole-account search is needed. 'senders' is the preferred fast first step for newsletter/junk cleanup because it aggregates frequent Inbox senders without repeated full-text scans. Account selection: without 'account', a folder id such as 'account2://INBOX' selects its own account, and a folder name that exists on several mail accounts is an error, not a silent choice of the first IMAP account; then pass folder as the exact folder.id (or full path) returned by 'folders', or name the account. Local Folders (type none) is not part of that check: a name present in both the IMAP account and Local Folders silently resolves to the IMAP account, so for Local Folders work always pass account:'Local Folders' or the account2://... folder id. 'folders' returns totalMessageCount/unreadMessageCount; include_unique_counts:true (optionally with folder) adds messageRecords, uniqueMessageCount and duplicateRecords but scans every header. " +
			"Write operations: 'create_folder' (name = new folder name, account = REQUIRED target account such as 'Local Folders', parent = optional parent), 'rename_folder' and 'delete_folder' (folder = exact folder.id such as account2://Name, or a name plus account) manage Thunderbird folders; 'copy' (folder = source, destination + destination_account = target) leaves the originals untouched: to delete the copies afterwards, trash/purge in the copy DESTINATION folder, never in the copy source; system/special folders are protected from rename/delete. 'compact_folder' runs Thunderbird's native maintenance on one folder, physically reclaiming local mbox space from messages already removed without deleting current messages; run it once after cleanup, not after every batch. 'move' moves messages between ANY Thunderbird folders/labels (for example Inbox -> Ważne or Ważne -> another folder); source is 'folder' and target is 'destination'. On Gmail, trash/move from a label other than Inbox/Trash/Spam/All Mail is refused without changes when a selected message also carries Inbox or another label; for move, allow_other_labels:true accepts removing only the source label. Every move is verified: status 'verified_moved' or 'moved_but_unverified' (movedButUnverified, missingInDestination; no continuation is returned then) - never report an unverified batch as done. IMAP moves also return recheckAfterMs and a ready 'recheck' request: send it as op 'verify_move' after that delay (read-only; reports locations, onlyInAllMail when Gmail dropped the label). 'repair_folder' (folder) runs Thunderbird's Repair Folder to rebuild an index with duplicate records without deleting messages; it refuses busy/offline/open folders (try again later, no automatic retry), Gmail All Mail needs allow_all_mail:true, and status.nativeRepairFolderAvailable tells whether it is installed. 'delete_folder' may report deleted:false, movedToTrash:true with newPath when Thunderbird (Local Folders) moved the folder into Trash instead. 'import_msg' uses the installed classic Outlook COM reader to convert one local Outlook .msg file to RFC 822/MIME .eml locally (including body and attachments) and imports it into an explicitly selected Thunderbird folder. 'trash' moves filtered messages from Inbox (or an explicitly selected folder) to Thunderbird's real Trash; 'restore' moves filtered messages from Thunderbird's default Trash back to Inbox by default (or to an explicitly selected destination folder); 'purge'/'delete_permanently' permanently deletes only filtered messages already in Thunderbird's default Trash; 'empty_trash' permanently empties the entire default Trash using Thunderbird core's native IMAP EmptyTrash/DeleteAllMessages operation. " +
			"Address-book writes: 'create_address_book' creates a separate local book, 'add_contacts' adds an explicit list of name/email pairs, and 'update_contact' corrects one existing card. add_contacts deduplicates email addresses inside the selected destination book and is safe to retry while still allowing a complete dedicated book. Filter automated/noreply/store addresses out of contact_candidates before importing. " +
			"Bulk filtered writes are intentionally paged: one call processes up to batch_size messages (default 250, max 500). If the result has more:true, immediately repeat the SAME approved operation and filters with the returned continuation token; this avoids timeouts and loop protection. The continuation of an approved batch is not asked again. " +
			"For whole-Trash cleanup, prefer empty_trash instead of simulating 'all' with date filters. empty_trash is one server-side operation and only counts as success when it returns serverVerified:true. For purge/delete_permanently, intermediate IMAP batches are not server-confirmed and MUST NOT be reported as final success; the final IMAP batch must return serverVerified:true, while Local Folders use localVerified:true because no mail server exists. " +
			"APPROVAL: every write operation is shown to the user by SuperCli itself, which asks for approval before anything runs; you never set confirm (it is ignored and set by the host only after the user approves). Do not ask for permission in chat first: call the operation and the user gets an approval card. If the result says the user declined it in SuperCli or approval is unavailable, nothing was changed and Thunderbird was never called (it did not refuse anything): do not retry, tell the user. move requires destination and either at least one filter or all:true; all:true is only for an explicitly approved whole-folder move. import_msg requires path to a local .msg file plus destination; because importing to an IMAP folder can upload the historical message to the mail server, never guess the target folder. empty_trash is irreversible. restore is recoverable and only reads from the default Trash. Use count before destructive cleanup when the local Trash count is trustworthy, but empty_trash can still repair a diverged Gmail Trash when Thunderbird's local cache incorrectly shows 0. A bridge error with notExecuted:true / 'NOT executed' means Thunderbird refused before running anything: it is safe to call again later (a new request id is used). An 'outcome unknown' error means it may have executed: check mailbox state before repeating a mutation. The bridge extension must be installed and Thunderbird must be running for mailbox operations.",
		Schema: `{
  "type": "object",
  "properties": {
    "op":       {"type": "string", "enum": ["status", "accounts", "folders", "count", "search", "read", "attachments", "get_attachment", "senders", "largest", "verify_move", "address_books", "contacts", "contact_candidates", "create_address_book", "add_contacts", "update_contact", "create_folder", "rename_folder", "delete_folder", "compact_folder", "repair_folder", "copy", "move", "import_msg", "trash", "restore", "purge", "delete_permanently", "empty_trash"], "description": "Operation to perform. search/count/largest support address, from, to, subject, text and date filters. contact_candidates aggregates sender/recipient addresses from message headers. address_books/contacts inspect Thunderbird contacts. create_address_book/add_contacts/update_contact maintain a local address book after confirmation. largest returns a compact top list by message size. copy safely archives matching messages to another folder and verifies Message-ID. read retrieves a decoded message body by message_id without changing read state. folders reports folder capabilities. create_folder/rename_folder/delete_folder manage folders. compact_folder reclaims local mbox space for one folder without deleting current messages. repair_folder rebuilds one folder index (confirm:true). verify_move re-checks a finished move (read-only). get_attachment retrieves actual attachment content and directly attaches common image formats for vision-capable models; non-image files return a temporary localPath."}, 
    "account":  {"type": "string", "description": "Optional Thunderbird account id or name (exact name first, then a unique substring). When omitted, a folder id selects its own account; a folder name present on several accounts is an error (use folder.id from 'folders'); otherwise the first IMAP account."},
    "folder":   {"type": "string", "description": "Optional source folder id/name/path; prefer the exact folder.id returned by 'folders' when a name is ambiguous. Required by compact_folder and repair_folder. move can use any Thunderbird folder/label (for example Odebrane, Ważne, a custom folder). For restore, source must be the default Trash."},
    "destination": {"type": "string", "description": "move/import_msg/verify_move: required destination folder id/name/path. restore: optional destination, default Inbox/Odebrane."},
    "destination_account": {"type": "string", "description": "copy: destination account id/name, for example Local Folders."},
    "path":     {"type": "string", "description": "import_msg only: absolute or working-directory-relative path to one local Microsoft Outlook .msg file. Current converter uses classic desktop Outlook COM on Windows."},
    "message_id": {"type": "integer", "description": "read/attachments/get_attachment: Thunderbird message id returned by search."},
    "message_ids": {"type": "array", "items": {"type": "integer"}, "maxItems": 100, "description": "Exact Thunderbird message records for bounded copy/cleanup when RFC Message-ID is duplicated."},
    "max_chars": {"type": "integer", "minimum": 500, "maximum": 100000, "description": "read only: maximum body characters returned in this call. Default 12000 to protect local-model context."},
    "start_char": {"type": "integer", "minimum": 0, "description": "read only: character offset. When hasMore:true, repeat with nextStartChar."},
    "part_name": {"type": "string", "description": "get_attachment: attachment partName returned by search/attachments. If omitted, attachment_name may be used; if the message has exactly one attachment, both may be omitted."},
    "attachment_name": {"type": "string", "description": "get_attachment: full attachment filename including extension, used when part_name is not supplied."},
    "from":     {"type": "string", "description": "Sender name, full sender email address, or a domain ('@example.com' = that domain and subdomains)."},
    "to":       {"type": "string", "description": "Recipient (To or Cc) name, full email address, or domain; also a valid mutation filter."},
    "email":    {"type": "string", "description": "update_contact: corrected email address; omit to retain the current address."},
    "address":  {"type": "string", "description": "Email address, name or domain matched against sender OR recipient (overrides from/to). Useful when the user only knows an address and not the direction; also a valid mutation filter."},
    "include_attachments": {"type": "boolean", "description": "search only: also return hasAttachments/attachmentCount/attachments per result. Default false (slow: one MIME parse per result)."},
    "include_duplicates": {"type": "boolean", "description": "count/search/senders/largest: return one entry per Thunderbird record instead of one per Message-ID. Default false."},
    "include_unique_counts": {"type": "boolean", "description": "folders only: add messageRecords, uniqueMessageCount and duplicateRecords (scans every header; combine with folder to limit it)."},
    "allow_other_labels": {"type": "boolean", "description": "move only (Gmail): accept removing just the source label when selected messages also carry Inbox or other labels."},
    "allow_all_mail": {"type": "boolean", "description": "repair_folder only: required to repair Gmail All Mail."},
    "source":   {"type": "string", "description": "verify_move only: the move's source folder (taken from the recheck request)."},
    "refresh":  {"type": "boolean", "description": "verify_move only: refresh both folders from the server first. Default true."},
    "subject":  {"type": "string", "description": "search/count and filtered writes: case-insensitive subject substring."},
    "header_message_id": {"type": "string", "description": "Stable RFC Message-ID header; preferred for exact copy/offload and cleanup."},
    "header_message_ids": {"type": "array", "items": {"type": "string"}, "maxItems": 500, "description": "Exact RFC Message-ID values: up to 100 for one bounded verified mutation; verify_move (required there) accepts up to 500."},
    "address_book": {"type": "string", "description": "contacts/add_contacts: Thunderbird address-book id or exact name."},
    "contact_id": {"type": "string", "description": "update_contact: exact contact id returned by contacts."},
    "contacts": {"type": "array", "maxItems": 200, "items": {"type": "object", "properties": {"name": {"type": "string"}, "email": {"type": "string"}}, "required": ["email"]}, "description": "add_contacts: explicit filtered name/email pairs. Remove automated senders before import."},
    "min_count": {"type": "integer", "minimum": 1, "description": "contact_candidates: minimum number of sender/recipient appearances."},
    "local_backup_verified": {"type": "boolean", "description": "Explicitly confirms that exact messages selected from Gmail All Mail have already been independently verified in Local Folders."},
    "name":     {"type": "string", "description": "create_folder/create_address_book: new folder or address-book name. update_contact: corrected display name."},
    "new_name": {"type": "string", "description": "rename_folder: new folder name."},
    "parent":   {"type": "string", "description": "create_folder: optional parent folder id/name/path; omit to create at account root."},
    "text":     {"type": "string", "description": "Full-text search across subject, body and author."},
    "since":    {"type": "string", "description": "Only messages on/after YYYY-MM-DD."},
    "until":    {"type": "string", "description": "Only messages on/before YYYY-MM-DD."},
    "limit":    {"type": "integer", "description": "search: maximum previews; senders/contact_candidates: maximum groups. contact_candidates supports up to 500; search/senders up to 100."},
    "scope":    {"type": "string", "enum": ["inbox", "account"], "description": "count/search/senders/contact_candidates only. Default 'inbox'. Use 'account' only when a whole-account scan is explicitly needed; Gmail labels can duplicate a message across folders."},
    "confirm":  {"type": "boolean", "description": "Ignored: set by SuperCli only after the user approves the write in its approval prompt. Do not send it."},
    "all":      {"type": "boolean", "description": "move only: move the entire selected source folder when explicitly requested. Requires destination. Prefer filters for narrower moves."},
    "batch_size": {"type": "integer", "minimum": 1, "maximum": 500, "description": "move/trash/restore/purge only: messages processed in one call. Default 250. Keep the default unless a smaller batch is needed."},
    "continuation": {"type": "string", "description": "move/trash/restore/purge continuation token returned by a previous batch. When more:true, repeat the exact same operation and filters with this token."}
  },
  "required": ["op"]
}`,
		Fn:          t.execute,
		Approval:    t.approval,
		ApprovalArg: "confirm",
	}
}

func (t *ThunderbirdMail) execute(ctx context.Context, raw json.RawMessage) (Result, error) {
	var args thunderbirdToolArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: bad args: %w", err)}, nil
	}
	args.Op = strings.ToLower(strings.TrimSpace(args.Op))
	// The bridge ignores fields it does not know, so a guessed argument such as
	// "query" or "unread" silently widened a search to the whole folder and the
	// model reported unrelated totals. Refuse it before anything is sent.
	if err := thunderbirdRejectUnknownArgs(raw); err != nil {
		return Result{Err: err}, nil
	}
	switch args.Op {
	case "status", "accounts", "folders", "count", "search", "senders", "largest", "address_books", "contacts", "contact_candidates":
	case "verify_move":
		if strings.TrimSpace(args.Destination) == "" || len(args.HeaderMessageIDs) == 0 {
			return Result{Err: fmt.Errorf("thunderbird_mail: verify_move requires destination and header_message_ids (use the recheck request returned by move)")}, nil
		}
	case "repair_folder":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: repair_folder requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt")}, nil
		}
		if strings.TrimSpace(args.Folder) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: repair_folder requires folder")}, nil
		}
	case "read":
		if args.MessageID <= 0 {
			return Result{Err: fmt.Errorf("thunderbird_mail: read requires message_id returned by search")}, nil
		}
		if args.MaxChars != 0 && (args.MaxChars < 500 || args.MaxChars > 100000) {
			return Result{Err: fmt.Errorf("thunderbird_mail: read max_chars must be between 500 and 100000")}, nil
		}
		if args.StartChar < 0 {
			return Result{Err: fmt.Errorf("thunderbird_mail: read start_char cannot be negative")}, nil
		}
	case "create_folder":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: create_folder requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt")}, nil
		}
		if strings.TrimSpace(args.Name) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: create_folder requires name (the new folder's name; 'folder' is not used here - pass the parent folder as parent and the target account as account, e.g. account:'Local Folders')")}, nil
		}
	case "create_address_book":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: create_address_book requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt")}, nil
		}
		if strings.TrimSpace(args.Name) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: create_address_book requires name")}, nil
		}
	case "add_contacts":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: add_contacts requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt")}, nil
		}
		if strings.TrimSpace(args.AddressBook) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: add_contacts requires address_book")}, nil
		}
		if len(args.Contacts) == 0 || len(args.Contacts) > 200 {
			return Result{Err: fmt.Errorf("thunderbird_mail: add_contacts requires 1-200 contacts")}, nil
		}
	case "update_contact":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: update_contact requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt")}, nil
		}
		if strings.TrimSpace(args.ContactID) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: update_contact requires contact_id")}, nil
		}
		if strings.TrimSpace(args.Name) == "" && strings.TrimSpace(args.Email) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: update_contact requires name or email")}, nil
		}
	case "rename_folder":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: rename_folder requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt")}, nil
		}
		if strings.TrimSpace(args.Folder) == "" || strings.TrimSpace(args.NewName) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: rename_folder requires folder and new_name")}, nil
		}
	case "delete_folder":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: delete_folder requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt; deleting a folder can delete its messages")}, nil
		}
		if strings.TrimSpace(args.Folder) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: delete_folder requires folder")}, nil
		}
	case "compact_folder":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: compact_folder requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt")}, nil
		}
		if strings.TrimSpace(args.Folder) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: compact_folder requires folder")}, nil
		}
	case "attachments", "get_attachment":
		if args.MessageID <= 0 {
			return Result{Err: fmt.Errorf("thunderbird_mail: %s requires message_id returned by search", args.Op)}, nil
		}
	case "empty_trash":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: empty_trash requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt; this permanently empties the entire Trash")}, nil
		}
	case "import_msg":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: import_msg requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt")}, nil
		}
		if strings.TrimSpace(args.Path) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: import_msg requires path to a local .msg file")}, nil
		}
		if strings.TrimSpace(args.Destination) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: import_msg requires destination; do not guess whether historical mail should stay local or be uploaded to IMAP")}, nil
		}
	case "move":
		if err := validateThunderbirdMoveArgs(args); err != nil {
			return Result{Err: err}, nil
		}
	case "copy":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: copy requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt")}, nil
		}
		if strings.TrimSpace(args.Destination) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: copy requires destination")}, nil
		}
		if !thunderbirdHasMutationSelector(args) {
			return Result{Err: fmt.Errorf("thunderbird_mail: copy requires at least one filter")}, nil
		}
	case "trash", "restore", "purge", "delete_permanently":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: %s requires confirm:true, which only SuperCli sets after the user approves it in the approval prompt", args.Op)}, nil
		}
		if !thunderbirdHasMutationSelector(args) {
			return Result{Err: fmt.Errorf("thunderbird_mail: %s requires at least one filter (from/to/address/subject/text/since/until or exact message ids)", args.Op)}, nil
		}
	default:
		return Result{Err: fmt.Errorf("thunderbird_mail: unsupported op %q", args.Op)}, nil
	}

	raw, err := thunderbirdRouteAccounts(args, raw)
	if err != nil {
		return Result{Err: err}, nil
	}

	if err := globalThunderbirdBridge.configureDataDir(t.dataDir); err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: %w", err)}, nil
	}
	if args.Op == "import_msg" {
		return t.importMSG(ctx, args)
	}

	data, err := globalThunderbirdBridge.call(ctx, args.Op, raw)
	if err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: %w", err)}, nil
	}
	thunderbirdRememberContinuation(ctx, data)
	if args.Op == "get_attachment" {
		return t.attachmentResult(data)
	}
	note := ""
	switch args.Op {
	case "move":
		note = thunderbirdMoveFollowUp(data, time.Now())
	case "copy":
		note = thunderbirdCopyFollowUp(data)
	}
	return Result{Text: thunderbirdCompactResult(data) + note}, nil
}

// thunderbirdCompactResult re-encodes a bridge answer as compact JSON and drops
// fields that carry no information: folder "capabilities" objects whose values
// are all null (Thunderbird 128 does not report them) and empty cc/bcc/specialUse
// lists. Indented JSON with those fields roughly doubled every search/folders
// answer, pushing ordinary 10-20 message results past the host's inline output
// limit so small local models only saw a preview and had to page read_output.
func thunderbirdCompactResult(data json.RawMessage) string {
	var v any
	if json.Unmarshal(data, &v) != nil {
		return string(data)
	}
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false) // keep <Message-ID> brackets and & readable
	if enc.Encode(thunderbirdStripEmpty(v)) != nil {
		return string(data)
	}
	return strings.TrimSpace(out.String())
}

func thunderbirdStripEmpty(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			switch k {
			case "capabilities":
				if m, ok := child.(map[string]any); ok && thunderbirdAllNull(m) {
					delete(x, k)
					continue
				}
			case "bccList", "ccList", "specialUse":
				if list, ok := child.([]any); ok && len(list) == 0 {
					delete(x, k)
					continue
				}
			}
			x[k] = thunderbirdStripEmpty(child)
		}
		return x
	case []any:
		for i := range x {
			x[i] = thunderbirdStripEmpty(x[i])
		}
		return x
	}
	return v
}

func thunderbirdAllNull(m map[string]any) bool {
	for _, v := range m {
		if v != nil {
			return false
		}
	}
	return true
}

// thunderbirdFolderAccount returns the account id prefix of a folder id such as
// "account2://Trash", or "" for a plain name or path.
func thunderbirdFolderAccount(folder string) string {
	folder = strings.TrimSpace(folder)
	if i := strings.Index(folder, "://"); i > 0 && !strings.ContainsAny(folder[:i], "/ 	") {
		return folder[:i]
	}
	return ""
}

// thunderbirdRouteAccounts makes a destination folder id authoritative.
// The bridge resolves copy's destination inside destination_account, defaulting
// to the SOURCE account, so copy from Gmail to "account2://X" failed with
// "folder not found" although the folder existed (seen live with a local model,
// which then gave up). Other operations resolve the destination inside the
// source account only, so a cross-account destination is refused up front.
func thunderbirdRouteAccounts(args thunderbirdToolArgs, raw json.RawMessage) (json.RawMessage, error) {
	switch args.Op {
	case "create_folder", "rename_folder", "delete_folder":
		return thunderbirdRouteFolderAdmin(args, raw)
	}
	destAccount := thunderbirdFolderAccount(args.Destination)
	if destAccount == "" {
		return raw, nil
	}
	switch args.Op {
	case "copy":
		if strings.TrimSpace(args.DestinationAccount) != "" {
			return raw, nil
		}
		return thunderbirdWithField(raw, "destination_account", destAccount), nil
	case "move", "restore", "import_msg":
		srcAccount := thunderbirdFolderAccount(args.Folder)
		if srcAccount == "" && strings.HasPrefix(strings.TrimSpace(args.Account), "account") && !strings.ContainsAny(args.Account, " @") {
			srcAccount = strings.TrimSpace(args.Account)
		}
		if srcAccount != "" && srcAccount != destAccount {
			return nil, fmt.Errorf("thunderbird_mail: %s works inside one account, but folder is in %s and destination %q is in %s; nothing was executed. Use op 'copy' (with destination_account) to put messages into another account", args.Op, srcAccount, args.Destination, destAccount)
		}
	}
	return raw, nil
}

// thunderbirdRouteFolderAdmin never lets folder administration fall back to a
// guessed account. Live, a local model sent create_folder with
// folder:"Local Folders" and no account; the bridge ignores folder there and
// created the folder (a Gmail label) on the first IMAP account instead.
func thunderbirdRouteFolderAdmin(args thunderbirdToolArgs, raw json.RawMessage) (json.RawMessage, error) {
	account := strings.TrimSpace(args.Account)
	if args.Op == "create_folder" {
		if strings.TrimSpace(args.Folder) != "" {
			return nil, fmt.Errorf("thunderbird_mail: create_folder does not take folder (got %q); nothing was executed. Pass account (for example account:'Local Folders' or the account id from 'accounts'), name, and optionally parent", args.Folder)
		}
		if account != "" {
			return raw, nil
		}
		parentAccount := thunderbirdFolderAccount(args.Parent)
		if parentAccount == "" {
			return nil, fmt.Errorf("thunderbird_mail: create_folder requires account (for example 'Local Folders' or the IMAP account id from 'accounts') or a parent folder id such as account2://Name, so the folder is never created on a guessed account; nothing was executed")
		}
		return thunderbirdWithField(raw, "account", parentAccount), nil
	}
	if account == "" && thunderbirdFolderAccount(args.Folder) == "" {
		return nil, fmt.Errorf("thunderbird_mail: %s requires the exact folder.id from 'folders' (for example account2://Name) or an explicit account; a bare name resolves to the first IMAP account even when the folder meant is in Local Folders. Nothing was executed", args.Op)
	}
	return raw, nil
}

func thunderbirdWithField(raw json.RawMessage, name, value string) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw
	}
	fields[name], _ = json.Marshal(value)
	out, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return out
}

var (
	thunderbirdKnownArgsOnce sync.Once
	thunderbirdKnownArgs     map[string]bool
)

// thunderbirdArgHints maps arguments models commonly guess to the real ones.
var thunderbirdArgHints = map[string]string{
	"query":     "use from (sender name/address/domain), subject, to/address or text (full text, slow)",
	"q":         "use from, subject, to/address or text",
	"keyword":   "use from, subject or text",
	"sender":    "use from",
	"recipient": "use to",
	"unread":    "there is no unread filter; op 'folders' returns unreadMessageCount per folder",
	"is_unread": "there is no unread filter; op 'folders' returns unreadMessageCount per folder",
	"read":      "there is no read-state filter; op 'folders' returns unreadMessageCount per folder",
	"date_from": "use since (YYYY-MM-DD)",
	"date_to":   "use until (YYYY-MM-DD)",
	"after":     "use since (YYYY-MM-DD)",
	"before":    "use until (YYYY-MM-DD)",
	"max":       "use limit",
	"count":     "use limit (or op 'count')",
	"folder_id": "use folder",
	"target":    "use destination",
}

func thunderbirdRejectUnknownArgs(raw json.RawMessage) error {
	thunderbirdKnownArgsOnce.Do(func() {
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal([]byte(NewThunderbirdMail().Spec().Schema), &schema)
		thunderbirdKnownArgs = make(map[string]bool, len(schema.Properties))
		for name := range schema.Properties {
			thunderbirdKnownArgs[name] = true
		}
	})
	if len(thunderbirdKnownArgs) == 0 {
		return nil // schema unreadable: never block on our own bug
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil // the typed decode above already reported malformed input
	}
	var unknown []string
	for name := range fields {
		if !thunderbirdKnownArgs[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	var hints []string
	for _, name := range unknown {
		if hint := thunderbirdArgHints[strings.ToLower(name)]; hint != "" {
			hints = append(hints, fmt.Sprintf("%s: %s", name, hint))
		}
	}
	msg := fmt.Sprintf("thunderbird_mail: unknown argument(s) %s; nothing was executed", strings.Join(unknown, ", "))
	if len(hints) > 0 {
		msg += " (" + strings.Join(hints, "; ") + ")"
	}
	return errors.New(msg + ". Repeat the call with only documented arguments.")
}

func (b *thunderbirdBridgeState) ensureServer() error {
	b.once.Do(func() {
		ln, err := net.Listen("tcp", thunderbirdBridgeAddr)
		if err != nil {
			b.startErr = fmt.Errorf("cannot listen on %s: %w", thunderbirdBridgeAddr, err)
			return
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/poll", b.handlePoll)
		mux.HandleFunc("/result", b.handleResult)
		mux.HandleFunc("/health", b.handleHealth)
		mux.HandleFunc("/message-file", b.handleMessageFile)
		mux.HandleFunc("/attachment-file", b.handleAttachmentFile)
		server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = server.Serve(ln) }()
	})
	return b.startErr
}

func (b *thunderbirdBridgeState) authorized(r *http.Request) bool {
	return r.URL.Query().Get("token") == thunderbirdBridgeToken
}

func setThunderbirdCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Cache-Control", "no-store")
}

func (b *thunderbirdBridgeState) handlePoll(w http.ResponseWriter, r *http.Request) {
	setThunderbirdCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet || !b.authorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	b.mu.Lock()
	b.lastPoll = time.Now()
	b.activePolls++
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.activePolls--
		b.mu.Unlock()
	}()

	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case req := <-b.queue:
			b.mu.Lock()
			active := b.waiters[req.ID] != nil
			if active {
				if b.dispatched == nil {
					b.dispatched = make(map[string]bool)
				}
				b.dispatched[req.ID] = true
			}
			b.mu.Unlock()
			// A cancelled caller must not leave an unclaimed mutation in the queue.
			// Cancellation racing with dispatch still has an unknown outcome.
			if !active {
				continue
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(req)
			return
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (b *thunderbirdBridgeState) handleResult(w http.ResponseWriter, r *http.Request) {
	setThunderbirdCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost || !b.authorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	defer r.Body.Close()
	var response thunderbirdBridgeResponse
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&response); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	// Retain only a digest, never mailbox contents. A lost HTTP acknowledgement
	// can then be retried without delivering the operation's result twice.
	encoded, _ := json.Marshal(response)
	digest := sha256.Sum256(encoded)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.pruneCompletions(now)
	b.lastPoll = now
	if completion, ok := b.completed[response.ID]; ok {
		if completion.digest != digest {
			http.Error(w, "conflicting result for completed request", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	waiter := b.waiters[response.ID]
	if waiter == nil {
		http.Error(w, "unknown request", http.StatusNotFound)
		return
	}
	select {
	case waiter <- response:
		if b.completed == nil {
			b.completed = make(map[string]thunderbirdCompletion)
		}
		b.completionSequence++
		b.completed[response.ID] = thunderbirdCompletion{digest: digest, at: now, order: b.completionSequence}
		b.pruneCompletions(now)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "request already completed", http.StatusConflict)
	}
}

const thunderbirdCompletionTTL = 5 * time.Minute
const thunderbirdCompletionLimit = 256

type thunderbirdCompletion struct {
	digest [32]byte
	at     time.Time
	order  uint64
}

// Caller holds b.mu. This process-local acknowledgement window is not durable
// idempotency, and cannot recover outcomes after a host or extension restart.
func (b *thunderbirdBridgeState) pruneCompletions(now time.Time) {
	for id, completion := range b.completed {
		if now.Sub(completion.at) >= thunderbirdCompletionTTL {
			delete(b.completed, id)
		}
	}
	for len(b.completed) > thunderbirdCompletionLimit {
		var oldest string
		var at time.Time
		var order uint64
		for id, completion := range b.completed {
			if at.IsZero() || completion.at.Before(at) || (completion.at.Equal(at) && completion.order < order) {
				oldest, at, order = id, completion.at, completion.order
			}
		}
		delete(b.completed, oldest)
	}
}

func (b *thunderbirdBridgeState) handleHealth(w http.ResponseWriter, r *http.Request) {
	setThunderbirdCORS(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet || !b.authorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	b.mu.Lock()
	last := b.lastPoll
	active := b.activePolls
	b.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "last_poll": last, "active_polls": active})
}

func (b *thunderbirdBridgeState) call(ctx context.Context, op string, raw json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.ensureServer(); err != nil {
		return nil, err
	}
	id := fmt.Sprintf("tb-%d-%d", time.Now().UnixNano(), b.requestID.Add(1))
	waiter := make(chan thunderbirdBridgeResponse, 1)
	b.mu.Lock()
	b.waiters[id] = waiter
	lastPoll := b.lastPoll
	activePolls := b.activePolls
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.waiters, id)
		delete(b.dispatched, id)
		b.mu.Unlock()
	}()

	if activePolls == 0 && (lastPoll.IsZero() || time.Since(lastPoll) > 30*time.Second) {
		// Give a just-started Thunderbird extension a short chance to connect.
		// A long-poll can stay open for 20 seconds, so an active poll is itself
		// proof of liveness and must not be mistaken for a disconnect.
		deadline := time.NewTimer(4 * time.Second)
		defer deadline.Stop()
		for {
			b.mu.Lock()
			seen := b.lastPoll
			active := b.activePolls
			b.mu.Unlock()
			if active > 0 || (!seen.IsZero() && time.Since(seen) <= 30*time.Second) {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-deadline.C:
				return nil, fmt.Errorf("Thunderbird Bridge is not connected; install/enable SuperCLI Thunderbird Bridge and keep Thunderbird running")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req := thunderbirdBridgeRequest{ID: id, Op: op, Args: append(json.RawMessage(nil), raw...)}
	select {
	case b.queue <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(2 * time.Second):
		return nil, fmt.Errorf("Thunderbird Bridge request queue is busy")
	}

	select {
	case response := <-waiter:
		if !response.OK {
			return nil, thunderbirdResponseError(op, response)
		}
		return response.Data, nil
	case <-ctx.Done():
		return nil, b.abandonRequest(id, op, ctx.Err())
	case <-time.After(thunderbirdOperationTimeout(op)):
		return nil, b.abandonRequest(id, op, fmt.Errorf("timed out waiting for Thunderbird"))
	}
}

// Serialize cancellation with poll dispatch so a known unstarted request is
// removed before it can reach Thunderbird. Once dispatched, never infer failure.
func (b *thunderbirdBridgeState) abandonRequest(id, op string, cause error) error {
	b.mu.Lock()
	dispatched := b.dispatched[id]
	delete(b.waiters, id)
	delete(b.dispatched, id)
	b.mu.Unlock()
	if !dispatched {
		return fmt.Errorf("Thunderbird Bridge request %s (%s) cancelled before dispatch; operation not executed: %w", id, op, cause)
	}
	if thunderbirdIsReadOp(op) {
		return fmt.Errorf("Thunderbird Bridge did not answer %s (request %s) in time: %w. This is a read-only operation, so nothing in the mailbox changed; narrow it (a specific folder instead of scope:'account', since/until dates, from/subject instead of full-text text, a smaller limit) and try again", op, id, cause)
	}
	return fmt.Errorf("Thunderbird Bridge outcome unknown for %s (request %s); it may have executed; do not automatically retry mutations: %w", op, id, cause)
}

// thunderbirdReadTimeout exceeds the bridge's READ_OPERATION_TIMEOUT_MS (120 s)
// plus result delivery, so a slow read reports the bridge's own error.
const thunderbirdReadTimeout = 135 * time.Second

func thunderbirdIsReadOp(op string) bool {
	switch op {
	case "status", "accounts", "folders", "count", "search", "read", "attachments", "get_attachment", "senders", "largest", "verify_move", "address_books", "contacts", "contact_candidates":
		return true
	}
	return false
}

func thunderbirdOperationTimeout(op string) time.Duration {
	switch op {
	case "compact_folder", "empty_trash":
		return 15 * time.Minute
	case "repair_folder":
		// The extension allows 20 minutes for a folder index rebuild.
		return 20 * time.Minute
	case "move":
		// The bridge verifies each move twice (up to 15 s, then a 5-10 s settle
		// window and a server refresh of both folders) before answering.
		return 180 * time.Second
	case "count", "search", "read", "senders", "largest", "folders", "verify_move", "contact_candidates", "attachments", "get_attachment", "contacts", "address_books":
		// Reads: the extension stops a read after its own 120 s limit and answers
		// with a clear "read-only, narrow the query" error. Waiting slightly
		// longer lets that answer arrive instead of a host-side "outcome
		// unknown, it may have executed" that wrongly suggests a mutation.
		// attachments/get_attachment parse MIME of messages that can be 20-30 MB.
		return thunderbirdReadTimeout
	case "copy", "create_address_book", "add_contacts", "update_contact", "create_folder", "rename_folder", "delete_folder", "trash", "restore", "purge", "delete_permanently":
		// IMAP folder mutations are server operations. Gmail can take tens of
		// seconds to publish the completion event even when the operation is
		// healthy; the former 20 s default caused false failures and unsafe
		// retries while Thunderbird was still finishing the first request.
		return 120 * time.Second
	case "import_msg":
		return 120 * time.Second
	default:
		return 20 * time.Second
	}
}
