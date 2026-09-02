package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
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
type ThunderbirdMail struct{}

func NewThunderbirdMail() *ThunderbirdMail { return &ThunderbirdMail{} }

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
		strings.TrimSpace(args.Subject) != "" ||
		strings.TrimSpace(args.Text) != "" ||
		strings.TrimSpace(args.Since) != "" ||
		strings.TrimSpace(args.Until) != ""
}

func validateThunderbirdMoveArgs(args thunderbirdToolArgs) error {
	if !args.Confirm {
		return fmt.Errorf("thunderbird_mail: move requires confirm:true after explicit user approval")
	}
	if strings.TrimSpace(args.Destination) == "" {
		return fmt.Errorf("thunderbird_mail: move requires destination")
	}
	if !args.All && !thunderbirdHasMutationSelector(args) {
		return fmt.Errorf("thunderbird_mail: move requires at least one filter or exact identifier (from/subject/text/since/until/message_ids/header_message_id/header_message_ids) or all:true for an explicitly approved whole-folder move")
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
}

type thunderbirdBridgeState struct {
	once        sync.Once
	startErr    error
	queue       chan thunderbirdBridgeRequest
	mu          sync.Mutex
	waiters     map[string]chan thunderbirdBridgeResponse
	transfers   map[string]thunderbirdFileTransfer
	downloads   map[string]thunderbirdDownloadedAttachment
	lastPoll    time.Time
	activePolls int
	requestID   atomic.Uint64
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
			"Read operations: 'status', 'accounts', 'folders', 'count', 'search', 'read', 'attachments', 'get_attachment', 'senders', 'largest', 'address_books', 'contacts', and 'contact_candidates'. search/count can filter by sender (from), recipient (to), sender-or-recipient email address (address), subject substring, text and date. 'contact_candidates' aggregates sender and recipient addresses from message headers without loading bodies, excluding the mailbox owner's own address. 'largest' scans message headers locally and returns only the largest 1-100 messages with byte sizes and stable Message-ID headers, avoiding large AI context. Use 'read' with a message_id returned by search to retrieve the decoded body without changing read/unread state. read returns at most max_chars (default 12000); when hasMore:true, repeat with nextStartChar as start_char instead of loading a huge email into a small local model at once. search results include hasAttachments, attachmentCount and attachment metadata (full filename including extension, extension, MIME type, size, partName); 'attachments' lists the same metadata for one message_id. 'get_attachment' fetches the actual attachment bytes. For PNG/JPEG/GIF/WebP it returns a real image to the agent loop so a vision-capable model receives the pixels on the next model turn; for PDF/Office/other files it returns a temporary localPath so the appropriate document tool can read it. count/search/senders/largest default to Inbox to avoid Gmail label duplicates; use scope:'account' only when an explicit whole-account search is needed. 'senders' is the preferred fast first step for newsletter/junk cleanup because it aggregates frequent Inbox senders without repeated full-text scans. " +
			"Write operations: 'create_folder', 'rename_folder' and 'delete_folder' manage Thunderbird folders after confirm:true; system/special folders are protected from rename/delete. 'compact_folder' runs Thunderbird's native maintenance on one folder after confirm:true, physically reclaiming local mbox space from messages already removed without deleting current messages; run it once after cleanup, not after every batch. 'move' moves messages between ANY Thunderbird folders/labels (for example Inbox -> Ważne or Ważne -> another folder); source is 'folder' and target is 'destination'. 'import_msg' uses the installed classic Outlook COM reader to convert one local Outlook .msg file to RFC 822/MIME .eml locally (including body and attachments) and imports it into an explicitly selected Thunderbird folder. 'trash' moves filtered messages from Inbox (or an explicitly selected folder) to Thunderbird's real Trash; 'restore' moves filtered messages from Thunderbird's default Trash back to Inbox by default (or to an explicitly selected destination folder); 'purge'/'delete_permanently' permanently deletes only filtered messages already in Thunderbird's default Trash; 'empty_trash' permanently empties the entire default Trash using Thunderbird core's native IMAP EmptyTrash/DeleteAllMessages operation. " +
			"Address-book writes: 'create_address_book' creates a separate local book, 'add_contacts' adds an explicit list of name/email pairs, and 'update_contact' corrects one existing card after confirm:true. add_contacts deduplicates email addresses inside the selected destination book and is safe to retry while still allowing a complete dedicated book. Filter automated/noreply/store addresses out of contact_candidates before importing. " +
			"Bulk filtered writes are intentionally paged: one call processes up to batch_size messages (default 250, max 500). If the result has more:true, immediately repeat the SAME approved operation and filters with the returned continuation token; this avoids timeouts and loop protection. No new user confirmation is needed for continuation of the exact same approved cleanup. " +
			"For whole-Trash cleanup, prefer empty_trash instead of simulating 'all' with date filters. empty_trash is one server-side operation and only counts as success when it returns serverVerified:true. For purge/delete_permanently, intermediate IMAP batches are not server-confirmed and MUST NOT be reported as final success; the final IMAP batch must return serverVerified:true, while Local Folders use localVerified:true because no mail server exists. " +
			"move/trash/restore/import_msg and filtered purge require confirm:true after explicit user approval. move requires destination and either at least one filter or all:true; all:true is only for an explicitly approved whole-folder move. import_msg requires path to a local .msg file plus destination; because importing to an IMAP folder can upload the historical message to the mail server, never guess the target folder. empty_trash requires confirm:true and is irreversible. restore is recoverable and only reads from the default Trash. Use count before destructive cleanup when the local Trash count is trustworthy, but empty_trash can still repair a diverged Gmail Trash when Thunderbird's local cache incorrectly shows 0. The bridge extension must be installed and Thunderbird must be running for mailbox operations.",
		Schema: `{
  "type": "object",
  "properties": {
    "op":       {"type": "string", "enum": ["status", "accounts", "folders", "count", "search", "read", "attachments", "get_attachment", "senders", "largest", "address_books", "contacts", "contact_candidates", "create_address_book", "add_contacts", "update_contact", "create_folder", "rename_folder", "delete_folder", "compact_folder", "copy", "move", "import_msg", "trash", "restore", "purge", "delete_permanently", "empty_trash"], "description": "Operation to perform. search/count/largest support address, from, to, subject, text and date filters. contact_candidates aggregates sender/recipient addresses from message headers. address_books/contacts inspect Thunderbird contacts. create_address_book/add_contacts/update_contact maintain a local address book after confirmation. largest returns a compact top list by message size. copy safely archives matching messages to another folder and verifies Message-ID. read retrieves a decoded message body by message_id without changing read state. folders reports folder capabilities. create_folder/rename_folder/delete_folder manage folders. compact_folder reclaims local mbox space for one folder without deleting current messages. get_attachment retrieves actual attachment content and directly attaches common image formats for vision-capable models; non-image files return a temporary localPath."}, 
    "account":  {"type": "string", "description": "Optional Thunderbird account id or part of account name. Defaults to the first IMAP account."},
    "folder":   {"type": "string", "description": "Optional source folder id/name/path. Required by compact_folder. move can use any Thunderbird folder/label (for example Odebrane, Ważne, a custom folder). For restore, source must be the default Trash."},
    "destination": {"type": "string", "description": "move/import_msg: required destination folder id/name/path. restore: optional destination, default Inbox/Odebrane."},
    "destination_account": {"type": "string", "description": "copy: destination account id/name, for example Local Folders."},
    "path":     {"type": "string", "description": "import_msg only: absolute or working-directory-relative path to one local Microsoft Outlook .msg file. Current converter uses classic desktop Outlook COM on Windows."},
    "message_id": {"type": "integer", "description": "read/attachments/get_attachment: Thunderbird message id returned by search."},
    "message_ids": {"type": "array", "items": {"type": "integer"}, "maxItems": 100, "description": "Exact Thunderbird message records for bounded copy/cleanup when RFC Message-ID is duplicated."},
    "max_chars": {"type": "integer", "minimum": 500, "maximum": 100000, "description": "read only: maximum body characters returned in this call. Default 12000 to protect local-model context."},
    "start_char": {"type": "integer", "minimum": 0, "description": "read only: character offset. When hasMore:true, repeat with nextStartChar."},
    "part_name": {"type": "string", "description": "get_attachment: attachment partName returned by search/attachments. If omitted, attachment_name may be used; if the message has exactly one attachment, both may be omitted."},
    "attachment_name": {"type": "string", "description": "get_attachment: full attachment filename including extension, used when part_name is not supplied."},
    "from":     {"type": "string", "description": "Sender name or full sender email address."},
    "to":       {"type": "string", "description": "search/count: recipient name or full recipient email address."},
    "email":    {"type": "string", "description": "update_contact: corrected email address; omit to retain the current address."},
    "address":  {"type": "string", "description": "search/count: email address or name matched against sender OR To recipient. Useful when the user only knows an address and not the direction."},
    "subject":  {"type": "string", "description": "search/count and filtered writes: case-insensitive subject substring."},
    "header_message_id": {"type": "string", "description": "Stable RFC Message-ID header; preferred for exact copy/offload and cleanup."},
    "header_message_ids": {"type": "array", "items": {"type": "string"}, "maxItems": 100, "description": "Up to 100 exact RFC Message-ID values for one bounded verified cleanup."},
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
    "confirm":  {"type": "boolean", "description": "create_address_book/add_contacts/create_folder/rename_folder/delete_folder/compact_folder/move/import_msg/trash/restore/purge/empty_trash: must be true after explicit user approval. delete_folder may remove the folder and its contents; system/special folders are protected."},
    "all":      {"type": "boolean", "description": "move only: move the entire selected source folder when explicitly requested. Requires confirm:true and destination. Prefer filters for narrower moves."},
    "batch_size": {"type": "integer", "minimum": 1, "maximum": 500, "description": "move/trash/restore/purge only: messages processed in one call. Default 250. Keep the default unless a smaller batch is needed."},
    "continuation": {"type": "string", "description": "move/trash/restore/purge continuation token returned by a previous batch. When more:true, repeat the exact same operation and filters with this token."}
  },
  "required": ["op"]
}`,
		Fn: t.execute,
	}
}

func (t *ThunderbirdMail) execute(ctx context.Context, raw json.RawMessage) (Result, error) {
	var args thunderbirdToolArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: bad args: %w", err)}, nil
	}
	args.Op = strings.ToLower(strings.TrimSpace(args.Op))
	switch args.Op {
	case "status", "accounts", "folders", "count", "search", "senders", "largest", "address_books", "contacts", "contact_candidates":
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
			return Result{Err: fmt.Errorf("thunderbird_mail: create_folder requires confirm:true after explicit user approval")}, nil
		}
		if strings.TrimSpace(args.Name) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: create_folder requires name")}, nil
		}
	case "create_address_book":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: create_address_book requires confirm:true after explicit user approval")}, nil
		}
		if strings.TrimSpace(args.Name) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: create_address_book requires name")}, nil
		}
	case "add_contacts":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: add_contacts requires confirm:true after explicit user approval")}, nil
		}
		if strings.TrimSpace(args.AddressBook) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: add_contacts requires address_book")}, nil
		}
		if len(args.Contacts) == 0 || len(args.Contacts) > 200 {
			return Result{Err: fmt.Errorf("thunderbird_mail: add_contacts requires 1-200 contacts")}, nil
		}
	case "update_contact":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: update_contact requires confirm:true after explicit user approval")}, nil
		}
		if strings.TrimSpace(args.ContactID) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: update_contact requires contact_id")}, nil
		}
		if strings.TrimSpace(args.Name) == "" && strings.TrimSpace(args.Email) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: update_contact requires name or email")}, nil
		}
	case "rename_folder":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: rename_folder requires confirm:true after explicit user approval")}, nil
		}
		if strings.TrimSpace(args.Folder) == "" || strings.TrimSpace(args.NewName) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: rename_folder requires folder and new_name")}, nil
		}
	case "delete_folder":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: delete_folder requires confirm:true after explicit user approval; deleting a folder can delete its messages")}, nil
		}
		if strings.TrimSpace(args.Folder) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: delete_folder requires folder")}, nil
		}
	case "compact_folder":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: compact_folder requires confirm:true after explicit user approval")}, nil
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
			return Result{Err: fmt.Errorf("thunderbird_mail: empty_trash requires confirm:true after explicit user approval; this permanently empties the entire Trash")}, nil
		}
	case "import_msg":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: import_msg requires confirm:true after explicit user approval")}, nil
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
			return Result{Err: fmt.Errorf("thunderbird_mail: copy requires confirm:true after explicit user approval")}, nil
		}
		if strings.TrimSpace(args.Destination) == "" {
			return Result{Err: fmt.Errorf("thunderbird_mail: copy requires destination")}, nil
		}
		if !thunderbirdHasMutationSelector(args) {
			return Result{Err: fmt.Errorf("thunderbird_mail: copy requires at least one filter")}, nil
		}
	case "trash", "restore", "purge", "delete_permanently":
		if !args.Confirm {
			return Result{Err: fmt.Errorf("thunderbird_mail: %s requires confirm:true after explicit user approval", args.Op)}, nil
		}
		if !thunderbirdHasMutationSelector(args) {
			return Result{Err: fmt.Errorf("thunderbird_mail: %s requires at least one filter (from/subject/text/since/until)", args.Op)}, nil
		}
	default:
		return Result{Err: fmt.Errorf("thunderbird_mail: unsupported op %q", args.Op)}, nil
	}

	if args.Op == "import_msg" {
		return t.importMSG(ctx, args)
	}

	data, err := globalThunderbirdBridge.call(ctx, args.Op, raw)
	if err != nil {
		return Result{Err: fmt.Errorf("thunderbird_mail: %w", err)}, nil
	}
	if args.Op == "get_attachment" {
		return t.attachmentResult(data)
	}
	var pretty any
	if json.Unmarshal(data, &pretty) == nil {
		if formatted, err := json.MarshalIndent(pretty, "", "  "); err == nil {
			return Result{Text: string(formatted)}, nil
		}
	}
	return Result{Text: string(data)}, nil
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
	select {
	case req := <-b.queue:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(req)
	case <-timer.C:
		w.WriteHeader(http.StatusNoContent)
	case <-r.Context().Done():
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
	b.mu.Lock()
	waiter := b.waiters[response.ID]
	b.lastPoll = time.Now()
	b.mu.Unlock()
	if waiter == nil {
		http.Error(w, "unknown request", http.StatusNotFound)
		return
	}
	select {
	case waiter <- response:
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "request already completed", http.StatusConflict)
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
			if response.Error == "" {
				response.Error = "unknown Thunderbird extension error"
			}
			return nil, fmt.Errorf("%s", response.Error)
		}
		return response.Data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(thunderbirdOperationTimeout(op)):
		return nil, fmt.Errorf("Thunderbird Bridge timed out waiting for Thunderbird while running %s", op)
	}
}

func thunderbirdOperationTimeout(op string) time.Duration {
	switch op {
	case "compact_folder":
		return 15 * time.Minute
	case "count", "search", "read", "senders", "contact_candidates", "create_address_book", "add_contacts", "update_contact", "create_folder", "rename_folder", "delete_folder", "move", "trash", "restore", "purge", "delete_permanently":
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
