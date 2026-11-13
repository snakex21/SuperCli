package mail

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestThunderbirdRejectsUnknownArguments(t *testing.T) {
	tool := NewThunderbirdMail()
	for raw, want := range map[string]string{
		`{"op":"search","query":"discord","scope":"account"}`: "query: use from",
		`{"op":"count","unread":true}`:                        "op 'folders' returns unreadMessageCount",
		`{"op":"search","banana":1}`:                          "unknown argument(s) banana",
	} {
		res, err := tool.execute(context.Background(), json.RawMessage(raw))
		if err != nil || res.Err == nil {
			t.Fatalf("execute(%s) accepted: res=%+v err=%v", raw, res, err)
		}
		if msg := res.Err.Error(); !strings.Contains(msg, want) || !strings.Contains(msg, "nothing was executed") {
			t.Errorf("execute(%s) error %q lacks %q", raw, msg, want)
		}
	}
}

func TestThunderbirdKnownArgumentsStillPassValidation(t *testing.T) {
	// Every documented property must survive the unknown-argument check; the
	// call then fails later on its own guard, not on argument names.
	raw := `{"op":"trash","folder":"x","scope":"inbox","limit":5,"include_duplicates":true,"include_attachments":true,"min_count":1,"source":"a","refresh":true,"allow_other_labels":true,"allow_all_mail":true}`
	if err := thunderbirdRejectUnknownArgs(json.RawMessage(raw)); err != nil {
		t.Fatalf("documented arguments rejected: %v", err)
	}
	res, _ := NewThunderbirdMail().execute(context.Background(), json.RawMessage(raw))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "confirm:true") {
		t.Fatalf("expected the confirm guard, got %v", res.Err)
	}
}

func TestThunderbirdCreateFolderNameErrorExplainsFields(t *testing.T) {
	res, _ := NewThunderbirdMail().execute(context.Background(), json.RawMessage(`{"op":"create_folder","folder":"_X","account":"Local Folders","confirm":true}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "'folder' is not used") {
		t.Fatalf("create_folder error = %v", res.Err)
	}
}

func TestThunderbirdCompactResultDropsEmptyFields(t *testing.T) {
	data := json.RawMessage(`{"messages":[{"subject":"Zażółć <x>","ccList":[],"bccList":[],"ccListFull":[],"folder":{"id":"account1://[Gmail]/Ważne","specialUse":[],"capabilities":{"canAddMessages":null,"canBeDeleted":null}}}],"folder":{"specialUse":["inbox"],"capabilities":{"canBeDeleted":false}}}`)
	got := thunderbirdCompactResult(data)
	for _, gone := range []string{`"ccList"`, `"bccList"`, `"specialUse":[]`, `"canAddMessages"`, "\n"} {
		if strings.Contains(got, gone) {
			t.Errorf("compact result still contains %q: %s", gone, got)
		}
	}
	for _, kept := range []string{`"ccListFull":[]`, `"specialUse":["inbox"]`, `"canBeDeleted":false`, `Zażółć <x>`, `Ważne`} {
		if !strings.Contains(got, kept) {
			t.Errorf("compact result lost %q: %s", kept, got)
		}
	}
	if raw := thunderbirdCompactResult(json.RawMessage(`not json`)); raw != "not json" {
		t.Errorf("non-JSON answer must pass through, got %q", raw)
	}
}

func TestThunderbirdReadTimeoutOutlastsBridgeReadLimit(t *testing.T) {
	const bridgeReadLimit = 120 * time.Second
	for _, op := range []string{"count", "search", "read", "senders", "largest", "folders", "verify_move", "attachments", "get_attachment", "contacts", "contact_candidates"} {
		if got := thunderbirdOperationTimeout(op); got <= bridgeReadLimit {
			t.Errorf("%s timeout %s must exceed the bridge's own %s read limit", op, got, bridgeReadLimit)
		}
	}
	if got := thunderbirdOperationTimeout("trash"); got != 120*time.Second {
		t.Errorf("trash timeout changed to %s", got)
	}
}

func TestThunderbirdAbandonedReadIsReportedAsReadOnly(t *testing.T) {
	b := &thunderbirdBridgeState{waiters: map[string]chan thunderbirdBridgeResponse{}, dispatched: map[string]bool{"r1": true, "m1": true}}
	read := b.abandonRequest("r1", "search", errors.New("timed out waiting for Thunderbird"))
	if msg := read.Error(); strings.Contains(msg, "may have executed") || !strings.Contains(msg, "nothing in the mailbox changed") {
		t.Errorf("read timeout message = %q", msg)
	}
	mut := b.abandonRequest("m1", "trash", errors.New("timed out waiting for Thunderbird"))
	if !strings.Contains(mut.Error(), "outcome unknown") {
		t.Errorf("mutation timeout message = %q", mut.Error())
	}
}

func TestThunderbirdFolderLookupErrorsGetGuidance(t *testing.T) {
	for _, msg := range []string{"Nie znaleziono folderu Thunderbird: Trash", "Niejednoznaczna nazwa folderu; użyj folder.id lub pełnej ścieżki z operacji folders.", "Nie znaleziono folderu docelowego: X"} {
		err := thunderbirdResponseError("count", thunderbirdBridgeResponse{Error: msg})
		if !strings.Contains(err.Error(), "account:'Local Folders'") || !strings.HasPrefix(err.Error(), msg) {
			t.Errorf("error for %q = %q", msg, err)
		}
	}
	if err := thunderbirdResponseError("count", thunderbirdBridgeResponse{Error: "other"}); err.Error() != "other" {
		t.Errorf("unrelated error changed: %q", err)
	}
}

func TestThunderbirdCopyFollowUpNamesDestination(t *testing.T) {
	data := json.RawMessage(`{"source":{"id":"account1://Powiadomienia"},"destinationAccount":{"id":"account2","type":"none"},"destination":{"id":"account2://_T"},"verified":true}`)
	note := thunderbirdCopyFollowUp(data)
	for _, want := range []string{`"account2://_T"`, "never the copy source", "account:'Local Folders'"} {
		if !strings.Contains(note, want) {
			t.Errorf("copy note lacks %q: %s", want, note)
		}
	}
	if thunderbirdCopyFollowUp(json.RawMessage(`{"ok":true}`)) != "" {
		t.Error("note must be empty without source/destination")
	}
}

func TestThunderbirdSpecGuidesUnreadBrandAndLocalFolders(t *testing.T) {
	d := NewThunderbirdMail().Spec().Description
	for _, phrase := range []string{"NO unread/read filter", "unreadMessageCount", "from:'<brand>'", "scope:'account'", "Local Folders (type none) is not part of that check", "copy DESTINATION", "Unknown arguments are rejected"} {
		if !strings.Contains(d, phrase) {
			t.Errorf("description lacks %q", phrase)
		}
	}
}

func TestThunderbirdCopyRoutesDestinationAccountFromFolderID(t *testing.T) {
	raw := json.RawMessage(`{"op":"copy","confirm":true,"folder":"account1://Powiadomienia","destination":"account2://_T","message_ids":[1,2]}`)
	var args thunderbirdToolArgs
	_ = json.Unmarshal(raw, &args)
	routed, err := thunderbirdRouteDestinationAccount(args, raw)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(routed, &got)
	if got["destination_account"] != "account2" || got["destination"] != "account2://_T" {
		t.Fatalf("routed = %s", routed)
	}
	// An explicit destination_account wins.
	raw = json.RawMessage(`{"op":"copy","destination":"account2://_T","destination_account":"Local Folders"}`)
	_ = json.Unmarshal(raw, &args)
	if routed, _ := thunderbirdRouteDestinationAccount(args, raw); string(routed) != string(raw) {
		t.Fatalf("explicit destination_account rewritten: %s", routed)
	}
}

func TestThunderbirdMoveRefusesCrossAccountDestination(t *testing.T) {
	res, _ := NewThunderbirdMail().execute(context.Background(), json.RawMessage(`{"op":"move","confirm":true,"folder":"account1://INBOX","destination":"account2://Archiwum","from":"@example.com"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "works inside one account") {
		t.Fatalf("cross-account move error = %v", res.Err)
	}
	for _, raw := range []string{
		`{"op":"move","folder":"account1://INBOX","destination":"account1://Archiwum","from":"@example.com"}`,
		`{"op":"move","folder":"INBOX","destination":"account2://Archiwum","from":"@example.com"}`,
	} {
		var args thunderbirdToolArgs
		_ = json.Unmarshal([]byte(raw), &args)
		if _, err := thunderbirdRouteDestinationAccount(args, json.RawMessage(raw)); err != nil {
			t.Errorf("%s refused: %v", raw, err)
		}
	}
}
