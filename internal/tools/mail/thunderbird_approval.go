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
		Summary:        thunderbirdApprovalSummary(op, args, account, folder, destination),
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
		return t.approvalPreview(ctx, op, args, account, folder)
	}
	return req, nil
}

func thunderbirdApprovalSummary(op string, args thunderbirdToolArgs, account, folder, destination string) string {
	where := func(f string) string {
		if f == "" {
			return "the default folder"
		}
		return fmt.Sprintf("%q", f)
	}
	in := ""
	if account != "" {
		in = " in " + account
	}
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
		return fmt.Sprintf("Copy matching messages from %s%s to %q; the originals stay.", where(folder), in, destination)
	case "move":
		return fmt.Sprintf("Move matching messages from %s%s to %q.", where(folder), in, destination)
	case "import_msg":
		return fmt.Sprintf("Import Outlook message %q into %q%s (an IMAP folder uploads it to the mail server).", strings.TrimSpace(args.Path), destination, in)
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

// approvalPreview collects what the user needs to judge the request: the
// account name, how many messages are affected and a few senders/subjects.
// Only read-only bridge operations are used; failures leave fields unknown.
func (t *ThunderbirdMail) approvalPreview(ctx context.Context, op string, args thunderbirdToolArgs, account, folder string) core.ApprovalPreview {
	p := core.ApprovalPreview{Count: -1}
	if globalThunderbirdBridge.configureDataDir(t.dataDir) != nil {
		return p
	}
	if account != "" {
		if data, err := globalThunderbirdBridge.call(ctx, "accounts", json.RawMessage(`{"op":"accounts"}`)); err == nil {
			var accounts []struct{ ID, Name string }
			if json.Unmarshal(data, &accounts) == nil {
				for _, a := range accounts {
					if strings.EqualFold(a.ID, account) || strings.EqualFold(a.Name, account) {
						p.AccountName = a.Name
						break
					}
				}
			}
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
	case "copy", "move", "trash", "delete_folder":
	case "restore", "purge", "delete_permanently":
		if folder == "" {
			return p // the default Trash cannot be named in a read-only query
		}
	default:
		return p
	}
	if len(args.MessageIDs) > 0 {
		p.Count = len(args.MessageIDs)
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
	if data, err := globalThunderbirdBridge.call(ctx, "count", thunderbirdOpArgs("count", filter, 0)); err == nil {
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
	if data, err := globalThunderbirdBridge.call(ctx, "search", thunderbirdOpArgs("search", filter, 3)); err == nil {
		var out struct {
			Messages []struct {
				Author  string `json:"author"`
				Subject string `json:"subject"`
				Date    string `json:"date"`
			} `json:"messages"`
		}
		if json.Unmarshal(data, &out) == nil {
			for _, m := range out.Messages {
				date := m.Date
				if len(date) >= 10 {
					date = date[:10]
				}
				p.Samples = append(p.Samples, core.ApprovalSample{From: m.Author, Subject: m.Subject, Date: date})
			}
		}
	}
	return p
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
