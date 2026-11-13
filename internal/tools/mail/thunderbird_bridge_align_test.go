package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestThunderbirdOperationTimeoutsMatchBridgeWork(t *testing.T) {
	want := map[string]time.Duration{
		"copy": 120 * time.Second, "largest": 135 * time.Second, "folders": 135 * time.Second,
		"verify_move": 135 * time.Second, "search": 135 * time.Second, "count": 135 * time.Second,
		"move": 180 * time.Second, "repair_folder": 20 * time.Minute, "empty_trash": 15 * time.Minute,
		"compact_folder": 15 * time.Minute, "status": 20 * time.Second, "accounts": 20 * time.Second,
	}
	for op, d := range want {
		if got := thunderbirdOperationTimeout(op); got != d {
			t.Errorf("%s timeout = %s, want %s", op, got, d)
		}
	}
	if thunderbirdOperationTimeout("move") < 120*time.Second {
		t.Fatal("move must wait at least 120 s for the bridge's two-stage verification")
	}
}

func TestThunderbirdMutationSelectorAcceptsRecipientAndAddress(t *testing.T) {
	for _, args := range []thunderbirdToolArgs{{To: "@example.com"}, {Address: "person@example.com"}} {
		if !thunderbirdHasMutationSelector(args) {
			t.Fatalf("selector %+v rejected", args)
		}
		args.Confirm, args.Destination = true, "Archiwum"
		if err := validateThunderbirdMoveArgs(args); err != nil {
			t.Fatalf("move with %+v rejected: %v", args, err)
		}
	}
}

func TestThunderbirdNewOperationGuards(t *testing.T) {
	tool := NewThunderbirdMail()
	for raw, want := range map[string]string{
		`{"op":"verify_move","destination":"Archiwum"}`:             "requires destination and header_message_ids",
		`{"op":"verify_move","header_message_ids":["<a@example>"]}`: "requires destination and header_message_ids",
		`{"op":"repair_folder","folder":"INBOX"}`:                   "confirm:true",
		`{"op":"repair_folder","confirm":true}`:                     "requires folder",
	} {
		res, err := tool.execute(context.Background(), json.RawMessage(raw))
		if err != nil || res.Err == nil || !strings.Contains(res.Err.Error(), want) {
			t.Fatalf("execute(%s): res.Err=%v err=%v, want %q", raw, res.Err, err, want)
		}
	}
}

func TestThunderbirdSpecDocumentsBridgeContract(t *testing.T) {
	spec := NewThunderbirdMail().Spec()
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(spec.Schema), &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	ops := strings.Join(schema.Properties["op"].Enum, ",")
	for _, op := range []string{"verify_move", "repair_folder"} {
		if !strings.Contains(ops, op) {
			t.Errorf("op enum lacks %s", op)
		}
	}
	for _, p := range []string{"include_attachments", "include_duplicates", "include_unique_counts", "allow_other_labels", "allow_all_mail", "source", "refresh"} {
		if _, ok := schema.Properties[p]; !ok {
			t.Errorf("schema lacks %s", p)
		}
	}
	for _, phrase := range []string{"include_attachments:true", "folder.id", "'@example.com'", "allow_other_labels", "moved_but_unverified", "verify_move", "movedToTrash", "notExecuted", "include_duplicates", "repair_folder"} {
		if !strings.Contains(spec.Description, phrase) {
			t.Errorf("description lacks %q", phrase)
		}
	}
	if strings.Contains(spec.Description, "search results include hasAttachments") {
		t.Error("description still promises attachment metadata in every search")
	}
}

func TestThunderbirdMoveFollowUpSurfacesRecheckAndUnverified(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.Local)
	gmail := `{"status":"verified_moved","verified":true,"messageIds":["<a@example>"],"recheckAfterMs":180000,
	  "recheck":{"op":"verify_move","args":{"account":"account1","source":"INBOX","destination":"Archiwum","header_message_ids":["<a@example>"]}}}`
	note := thunderbirdMoveFollowUp(json.RawMessage(gmail), now)
	for _, frag := range []string{"verify_move", `"destination":"Archiwum"`, `"source":"INBOX"`, "after 3m0s", "12:03:00", "read-only"} {
		if !strings.Contains(note, frag) {
			t.Errorf("Gmail note lacks %q: %s", frag, note)
		}
	}
	if strings.Contains(note, "status moved_but_unverified") {
		t.Errorf("verified move reported as unverified: %s", note)
	}
	unverified := `{"status":"moved_but_unverified","movedButUnverified":3,"missingInDestination":["<a@x>","<b@x>"],"more":true,"recheckAfterMs":60000,
	  "recheck":{"op":"verify_move","args":{"destination":"Archiwum","header_message_ids":["<a@x>"]}}}`
	note = thunderbirdMoveFollowUp(json.RawMessage(unverified), now)
	for _, frag := range []string{"moved_but_unverified", "2 Message-ID(s)", "do not continue", "after 1m0s"} {
		if !strings.Contains(note, frag) {
			t.Errorf("unverified note lacks %q: %s", frag, note)
		}
	}
	local := `{"status":"verified_moved","verified":true,"recheckAfterMs":null}`
	if note := thunderbirdMoveFollowUp(json.RawMessage(local), now); note != "" {
		t.Errorf("Local Folders move needs no note, got %s", note)
	}
}

func TestThunderbirdResponseErrorGuidance(t *testing.T) {
	err := thunderbirdResponseError("move", thunderbirdBridgeResponse{NotExecuted: true, Error: "cache full: NOT executed"})
	if !strings.Contains(err.Error(), "safe to call it again later") || !strings.Contains(err.Error(), "NOT executed") {
		t.Errorf("notExecuted guidance missing: %v", err)
	}
	err = thunderbirdResponseError("repair_folder", thunderbirdBridgeResponse{Error: "Folder INBOX: inny proces ma do niego dostęp"})
	if !strings.Contains(err.Error(), "try repair_folder again in a few minutes") || !strings.Contains(err.Error(), "no automatic retry") {
		t.Errorf("busy guidance missing: %v", err)
	}
	if err = thunderbirdResponseError("count", thunderbirdBridgeResponse{Error: "boom"}); err.Error() != "boom" {
		t.Errorf("plain error changed: %v", err)
	}
	if err = thunderbirdResponseError("count", thunderbirdBridgeResponse{}); !strings.Contains(err.Error(), "unknown Thunderbird extension error") {
		t.Errorf("empty error: %v", err)
	}
}

// Results arrive as application/json; charset=utf-8 with Polish text and
// emoji in both data and errors; they must reach the caller unchanged.
func TestThunderbirdResultBodiesDecodeAsUTF8(t *testing.T) {
	for _, ok := range []bool{true, false} {
		t.Run(fmt.Sprintf("ok=%t", ok), func(t *testing.T) {
			b := newThunderbirdDeliveryTestState()
			type outcome struct {
				data json.RawMessage
				err  error
			}
			done := make(chan outcome, 1)
			go func() {
				data, err := b.call(context.Background(), "count", json.RawMessage(`{}`))
				done <- outcome{data, err}
			}()
			var req thunderbirdBridgeRequest
			select {
			case req = <-b.queue:
			case <-time.After(time.Second):
				t.Fatal("request not queued")
			}
			text := "Zażółć gęślą jaźń \U0001F4EC Ważne"
			body := fmt.Sprintf(`{"id":%q,"ok":%t,"data":{"folder":%q},"error":%q}`, req.ID, ok, text, text)
			httpReq := httptest.NewRequest(http.MethodPost, "/result?token="+thunderbirdBridgeToken, strings.NewReader(body))
			httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")
			rec := httptest.NewRecorder()
			b.handleResult(rec, httpReq)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			got := <-done
			if ok {
				var data struct{ Folder string }
				if got.err != nil || json.Unmarshal(got.data, &data) != nil || data.Folder != text {
					t.Fatalf("data=%s err=%v", got.data, got.err)
				}
			} else if got.err == nil || got.err.Error() != text {
				t.Fatalf("error=%v, want %q", got.err, text)
			}
		})
	}
}
