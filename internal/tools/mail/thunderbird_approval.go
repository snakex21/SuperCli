package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	core "supercli/internal/tools/core"
)

// thunderbirdOpRisk declares the risk of every mutating operation. Reads are
// absent and run without approval. The host gate (core.Tool.Approval) asks the
// user before any of these runs and only then sets confirm:true; a confirm the
// model sends is discarded.
var thunderbirdOpRisk = map[string]struct {
	risk       core.RiskLevel
	reversible bool
}{
	"create_folder":       {core.RiskWriteReversible, true},
	"rename_folder":       {core.RiskWriteReversible, true},
	"delete_folder":       {core.RiskDestructive, false},
	"compact_folder":      {core.RiskDestructive, false},
	"repair_folder":       {core.RiskWriteReversible, true},
	"copy":                {core.RiskWriteReversible, true},
	"move":                {core.RiskWriteReversible, true},
	"import_msg":          {core.RiskExternal, true},
	"trash":               {core.RiskWriteReversible, true},
	"restore":             {core.RiskWriteReversible, true},
	"purge":               {core.RiskDestructive, false},
	"delete_permanently":  {core.RiskDestructive, false},
	"empty_trash":         {core.RiskDestructive, false},
	"create_address_book": {core.RiskWriteReversible, true},
	"add_contacts":        {core.RiskWriteReversible, true},
	"update_contact":      {core.RiskWriteReversible, true},
}

// thunderbirdApprovedContinuations remembers continuation tokens returned by
// approved bulk batches. The bridge binds each token to the exact operation
// and filters, so presenting one is the same approved cleanup, not a new one.
var thunderbirdApprovedContinuations = struct {
	sync.Mutex
	tokens map[string]time.Time
}{tokens: map[string]time.Time{}}

const thunderbirdContinuationTTL = 30 * time.Minute

func thunderbirdRememberContinuation(ctx context.Context, data json.RawMessage) {
	if _, ok := core.ApprovalGranted(ctx); !ok {
		return
	}
	var out struct {
		Continuation string `json:"continuation"`
	}
	if json.Unmarshal(data, &out) != nil || strings.TrimSpace(out.Continuation) == "" {
		return
	}
	now := time.Now()
	thunderbirdApprovedContinuations.Lock()
	defer thunderbirdApprovedContinuations.Unlock()
	for token, at := range thunderbirdApprovedContinuations.tokens {
		if now.Sub(at) > thunderbirdContinuationTTL {
			delete(thunderbirdApprovedContinuations.tokens, token)
		}
	}
	thunderbirdApprovedContinuations.tokens[strings.TrimSpace(out.Continuation)] = now
}

func thunderbirdTakeContinuation(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	thunderbirdApprovedContinuations.Lock()
	defer thunderbirdApprovedContinuations.Unlock()
	at, ok := thunderbirdApprovedContinuations.tokens[token]
	if !ok || time.Since(at) > thunderbirdContinuationTTL {
		return false
	}
	delete(thunderbirdApprovedContinuations.tokens, token)
	return true
}

// approval is the trusted risk classifier registered as Tool.Approval.
func (t *ThunderbirdMail) approval(_ context.Context, raw json.RawMessage) (*core.ApprovalRequest, error) {
	var args thunderbirdToolArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, nil // execute reports malformed arguments without side effects
	}
	op := strings.ToLower(strings.TrimSpace(args.Op))
	risk, mutating := thunderbirdOpRisk[op]
	if !mutating {
		return nil, nil
	}
	account := strings.TrimSpace(args.Account)
	if account == "" {
		account = thunderbirdFolderAccount(args.Folder)
	}
	if account == "" && op == "create_folder" {
		account = thunderbirdFolderAccount(args.Parent)
	}
	folder := strings.TrimSpace(args.Folder)
	destination := strings.TrimSpace(args.Destination)
	if op == "copy" && strings.TrimSpace(args.DestinationAccount) != "" && thunderbirdFolderAccount(destination) == "" {
		destination = strings.TrimSpace(args.DestinationAccount) + "/" + destination
	}
	target := ""
	switch op {
	case "create_folder":
		folder = strings.TrimSpace(args.Parent)
		target = strings.TrimSpace(args.Name)
	case "rename_folder":
		target = strings.TrimSpace(args.NewName)
	case "create_address_book":
		target = strings.TrimSpace(args.Name)
	case "add_contacts":
		target = strings.TrimSpace(args.AddressBook)
	case "update_contact":
		target = strings.TrimSpace(args.ContactID)
	case "import_msg":
		target = strings.TrimSpace(args.Path)
	}
	req := &core.ApprovalRequest{
		Tool: "thunderbird_mail", Operation: op, Risk: risk.risk, Reversible: risk.reversible,
		Account: account, Folder: folder, Destination: destination, Target: target,
		Summary:        thunderbirdApprovalSummary(op, args, account, folder, destination, nil),
		NoSessionGrant: !risk.reversible,
	}
	scopeFolder := folder
	if op == "add_contacts" || op == "update_contact" || op == "create_address_book" {
		scopeFolder = target
	}
	req.Scope = []string{"thunderbird_mail", op, account, scopeFolder}
	if destination != "" {
		req.Scope = append(req.Scope, destination)
	}
	if strings.TrimSpace(args.Continuation) != "" && thunderbirdTakeContinuation(args.Continuation) {
		req.PreApproved = "continuation of an approved batch"
		return req, nil
	}
	req.Preview = func(ctx context.Context) core.ApprovalPreview {
		return t.approvalPreview(ctx, op, args, account, folder, destination)
	}
	return req, nil
}

// thunderbirdApprovalSummary describes the effect in one sentence. name maps
// an account id ("account2") to its display name; nil keeps the ids (the
// classifier runs without I/O, the preview re-renders with names).
func thunderbirdApprovalSummary(op string, args thunderbirdToolArgs, account, folder, destination string, name func(string) string) string {
	if name == nil {
		name = func(id string) string { return id }
	}
	// label quotes a folder or destination. A folder id of a named account
	// ("account2://INBOX") is shown as its path plus the account name.
	label := func(f, extraAccount string) string {
		acct, rest := thunderbirdFolderAccount(f), ""
		if acct != "" {
			rest = f[len(acct)+len("://"):]
		} else if extraAccount != "" && strings.HasPrefix(strings.ToLower(f), strings.ToLower(extraAccount)+"/") {
			acct, rest = extraAccount, f[len(extraAccount)+1:]
		}
		if acct == "" || name(acct) == acct {
			return fmt.Sprintf("%q", f)
		}
		if strings.EqualFold(acct, account) {
			return fmt.Sprintf("%q", rest)
		}
		return fmt.Sprintf("%q in %s", rest, name(acct))
	}
	where := func(f string) string {
		if f == "" {
			return "the default folder"
		}
		return label(f, "")
	}
	in := ""
	if account != "" {
		in = " in " + name(account)
	}
	dest := label(destination, strings.TrimSpace(args.DestinationAccount))
	switch op {
	case "create_folder":
		parent := ""
		if folder != "" {
			parent = " under " + where(folder)
		}
		return fmt.Sprintf("Create folder %q%s%s.", strings.TrimSpace(args.Name), parent, in)
	case "rename_folder":
		return fmt.Sprintf("Rename folder %s to %q%s.", where(folder), strings.TrimSpace(args.NewName), in)
	case "delete_folder":
		return fmt.Sprintf("Delete folder %s%s, including the messages inside it.", where(folder), in)
	case "compact_folder":
		return fmt.Sprintf("Compact folder %s%s: permanently reclaims space of already deleted messages.", where(folder), in)
	case "repair_folder":
		return fmt.Sprintf("Repair (rebuild the index of) folder %s%s; messages are not deleted.", where(folder), in)
	case "copy":
		return fmt.Sprintf("Copy matching messages from %s%s to %s; the originals stay.", where(folder), in, dest)
	case "move":
		return fmt.Sprintf("Move matching messages from %s%s to %s.", where(folder), in, dest)
	case "import_msg":
		return fmt.Sprintf("Import Outlook message %q into %s%s (an IMAP folder uploads it to the mail server).", strings.TrimSpace(args.Path), dest, in)
	case "trash":
		return fmt.Sprintf("Move matching messages from %s%s to Trash.", where(folder), in)
	case "restore":
		return fmt.Sprintf("Restore matching messages from Trash%s.", in)
	case "purge", "delete_permanently":
		return fmt.Sprintf("PERMANENTLY delete matching messages from Trash%s.", in)
	case "empty_trash":
		return fmt.Sprintf("PERMANENTLY empty the whole Trash%s.", in)
	case "create_address_book":
		return fmt.Sprintf("Create address book %q.", strings.TrimSpace(args.Name))
	case "add_contacts":
		return fmt.Sprintf("Add %d contact(s) to address book %q.", len(args.Contacts), strings.TrimSpace(args.AddressBook))
	case "update_contact":
		return fmt.Sprintf("Update contact %q.", strings.TrimSpace(args.ContactID))
	}
	return "Change Thunderbird data with " + op + "."
}

// thunderbirdPreviewIDLimit bounds how many exact message ids the preview
// resolves (one read-only bridge call each); the bridge itself re-checks every
// id before it changes anything.
const thunderbirdPreviewIDLimit = 10

type thunderbirdPreviewAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type thunderbirdPreviewFolder struct {
	ID         string   `json:"id"`
	AccountID  string   `json:"accountId"`
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	SpecialUse []string `json:"specialUse"`
}

type thunderbirdPreviewMessage struct {
	ID      int64                     `json:"id"`
	Author  string                    `json:"author"`
	Subject string                    `json:"subject"`
	Date    string                    `json:"date"`
	Folder  *thunderbirdPreviewFolder `json:"folder"`
}

type thunderbirdPreviewCall func(ctx context.Context, op string, raw json.RawMessage) (json.RawMessage, error)

func (t *ThunderbirdMail) previewBridge() thunderbirdPreviewCall {
	if t.previewCall != nil {
		return t.previewCall
	}
	if globalThunderbirdBridge.configureDataDir(t.dataDir) != nil {
		return nil
	}
	return globalThunderbirdBridge.call
}

// approvalPreview collects what the user needs to judge the request: the
// account display name, how many messages are affected, a few senders/subjects
// and warnings (exact message ids that are not in the stated folder). Only
// read-only bridge operations are used; failures leave fields unknown.
func (t *ThunderbirdMail) approvalPreview(ctx context.Context, op string, args thunderbirdToolArgs, account, folder, destination string) core.ApprovalPreview {
	p := core.ApprovalPreview{Count: -1}
	call := t.previewBridge()
	if call == nil {
		return p
	}
	var accounts []thunderbirdPreviewAccount
	connected := false
	if data, err := call(ctx, "accounts", json.RawMessage(`{"op":"accounts"}`)); err == nil && json.Unmarshal(data, &accounts) == nil {
		connected = true
	}
	accountID := ""
	names := map[string]string{}
	for _, a := range accounts {
		if a.ID != "" && a.Name != "" {
			names[strings.ToLower(a.ID)] = a.Name
		}
		if account != "" && (strings.EqualFold(a.ID, account) || strings.EqualFold(a.Name, account)) {
			p.AccountName, accountID = a.Name, a.ID
		}
	}
	accountName := func(id string) string {
		if n, ok := names[strings.ToLower(strings.TrimSpace(id))]; ok {
			return n
		}
		return id
	}
	if len(names) > 0 {
		named := thunderbirdApprovalSummary(op, args, account, folder, destination, accountName)
		if named != thunderbirdApprovalSummary(op, args, account, folder, destination, nil) {
			p.Summary = named
		}
	}
	switch op {
	case "add_contacts":
		p.Count = len(args.Contacts)
		for i, c := range args.Contacts {
			if i == 3 {
				break
			}
			p.Samples = append(p.Samples, core.ApprovalSample{From: strings.TrimSpace(c.Name + " <" + c.Email + ">")})
		}
		return p
	case "copy", "move", "trash", "restore", "purge", "delete_permanently":
		if len(args.MessageIDs) > 0 {
			p.Count = len(args.MessageIDs)
			if connected {
				previewThunderbirdMessageIDs(ctx, call, &p, op, args.MessageIDs, folder, accountID, accountName)
			}
			return p
		}
		if (op == "restore" || op == "purge" || op == "delete_permanently") && folder == "" {
			return p // the default Trash cannot be named in a read-only query
		}
	case "delete_folder":
	default:
		return p
	}
	if len(args.HeaderMessageIDs) > 1 {
		p.Count = len(args.HeaderMessageIDs)
		return p
	}
	filter := map[string]any{}
	if account != "" {
		filter["account"] = account
	}
	if folder != "" {
		filter["folder"] = folder
	}
	for name, value := range map[string]string{
		"from": args.From, "to": args.To, "address": args.Address, "subject": args.Subject,
		"text": args.Text, "since": args.Since, "until": args.Until, "header_message_id": args.HeaderMessageID,
	} {
		if v := strings.TrimSpace(value); v != "" {
			filter[name] = v
		}
	}
	if len(args.HeaderMessageIDs) == 1 {
		filter["header_message_id"] = args.HeaderMessageIDs[0]
	}
	wholeFolder := op == "delete_folder" || op == "move" && args.All
	if !wholeFolder && len(filter) == len(thunderbirdScopeKeys(filter)) {
		return p // no message filter: the operation itself will refuse
	}
	if data, err := call(ctx, "count", thunderbirdOpArgs("count", filter, 0)); err == nil {
		var out struct {
			Count int `json:"count"`
		}
		if json.Unmarshal(data, &out) == nil {
			p.Count = out.Count
		}
	}
	if p.Count == 0 {
		return p
	}
	if data, err := call(ctx, "search", thunderbirdOpArgs("search", filter, 3)); err == nil {
		var out struct {
			Messages []thunderbirdPreviewMessage `json:"messages"`
		}
		if json.Unmarshal(data, &out) == nil {
			for _, m := range out.Messages {
				p.Samples = append(p.Samples, thunderbirdPreviewSample(m))
			}
		}
	}
	return p
}

func thunderbirdPreviewSample(m thunderbirdPreviewMessage) core.ApprovalSample {
	date := m.Date
	if len(date) >= 10 {
		date = date[:10]
	}
	return core.ApprovalSample{From: m.Author, Subject: m.Subject, Date: date}
}

// previewThunderbirdMessageIDs resolves exact Thunderbird message ids with the
// read-only attachments operation (headers and attachment metadata, never the
// body). The first three found become samples; a checked id that is unknown
// or lives in another folder than the request names becomes a warning,
// because the bridge refuses the whole request in that case.
func previewThunderbirdMessageIDs(ctx context.Context, call thunderbirdPreviewCall, p *core.ApprovalPreview, op string, ids []int64, folder, accountID string, accountName func(string) string) {
	defaultFolder, defaultLabel := "inbox", "the default Inbox"
	if op == "restore" || op == "purge" || op == "delete_permanently" {
		defaultFolder, defaultLabel = "trash", "the default Trash"
	}
	stated := defaultLabel
	if folder != "" {
		stated = fmt.Sprintf("%q", folder)
	}
	var missing, elsewhere []string
	checked := 0
	for _, id := range ids {
		if checked == thunderbirdPreviewIDLimit || ctx.Err() != nil {
			break
		}
		raw, _ := json.Marshal(map[string]any{"op": "attachments", "message_id": id})
		data, err := call(ctx, "attachments", raw)
		if ctx.Err() != nil {
			break // a timeout says nothing about the id
		}
		checked++
		var out struct {
			Message *thunderbirdPreviewMessage `json:"message"`
		}
		if err != nil || json.Unmarshal(data, &out) != nil || out.Message == nil {
			missing = append(missing, fmt.Sprint(id))
			continue
		}
		m := *out.Message
		if len(p.Samples) < 3 {
			p.Samples = append(p.Samples, thunderbirdPreviewSample(m))
		}
		if m.Folder == nil || thunderbirdPreviewFolderMatches(*m.Folder, folder, defaultFolder, accountID) {
			continue
		}
		where := m.Folder.Path
		if where == "" {
			where = m.Folder.Name
		}
		subject := m.Subject
		if subject == "" {
			subject = "(no subject)"
		}
		elsewhere = append(elsewhere, fmt.Sprintf("%d %q is in %q (%s)", id, subject, where, accountName(m.Folder.AccountID)))
	}
	if len(elsewhere) > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("Not in %s: message %s. Thunderbird will refuse the whole request.", stated, thunderbirdJoinLimited(elsewhere, 3)))
	}
	if len(missing) > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("Thunderbird does not know message id %s (moved, deleted or never existed); the request will be refused.", thunderbirdJoinLimited(missing, 5)))
	}
	if checked < len(ids) && len(p.Warnings) > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("Only the first %d of %d message ids were checked.", checked, len(ids)))
	}
}

func thunderbirdJoinLimited(items []string, limit int) string {
	if len(items) <= limit {
		return strings.Join(items, "; ")
	}
	return fmt.Sprintf("%s; and %d more", strings.Join(items[:limit], "; "), len(items)-limit)
}

// thunderbirdPreviewFolderMatches mirrors the bridge's folder resolution
// (resolveFolder: id, full path, then a name or path suffix) and its default
// source folder (Inbox, or Trash for restore/purge).
func thunderbirdPreviewFolderMatches(f thunderbirdPreviewFolder, folder, defaultFolder, accountID string) bool {
	if accountID != "" && f.AccountID != "" && f.AccountID != accountID {
		return false
	}
	slash := func(v string) string { return strings.ReplaceAll(v, `\`, "/") }
	name := strings.ToLower(f.Name)
	path := strings.ToLower(slash(f.Path))
	wanted := strings.TrimSpace(folder)
	if wanted == "" {
		for _, use := range f.SpecialUse {
			if strings.EqualFold(use, defaultFolder) {
				return true
			}
		}
		fallback := map[string][]string{"inbox": {"inbox", "odebrane"}, "trash": {"trash", "kosz"}}[defaultFolder]
		for _, n := range fallback {
			if name == n || strings.HasSuffix(path, "/"+n) {
				return true
			}
		}
		return false
	}
	if f.ID == wanted || slash(f.Path) == slash(wanted) {
		return true
	}
	needle := strings.ToLower(slash(wanted))
	return name == needle || path == needle || strings.HasSuffix(path, "/"+needle)
}

func thunderbirdScopeKeys(filter map[string]any) []string {
	var keys []string
	for _, k := range []string{"account", "folder"} {
		if _, ok := filter[k]; ok {
			keys = append(keys, k)
		}
	}
	return keys
}

func thunderbirdOpArgs(op string, filter map[string]any, limit int) json.RawMessage {
	m := map[string]any{"op": op}
	for k, v := range filter {
		m[k] = v
	}
	if limit > 0 {
		m["limit"] = limit
	}
	out, _ := json.Marshal(m)
	return out
}
