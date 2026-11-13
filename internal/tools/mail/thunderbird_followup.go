package mail

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// thunderbirdResponseError turns an ok:false bridge answer into the error
// shown to the model, adding the retry guidance the bridge protocol defines.
func thunderbirdResponseError(op string, response thunderbirdBridgeResponse) error {
	msg := strings.TrimSpace(response.Error)
	if msg == "" {
		msg = "unknown Thunderbird extension error"
	}
	switch {
	case response.NotExecuted:
		return fmt.Errorf("%s (Thunderbird did NOT execute %s; it is safe to call it again later, which uses a new request id)", msg, op)
	case op == "repair_folder" && thunderbirdFolderBusy(msg):
		return fmt.Errorf("%s (folder is busy; nothing was changed; try repair_folder again in a few minutes, no automatic retry)", msg)
	}
	return errors.New(msg)
}

func thunderbirdFolderBusy(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "inny proces ma do niego dostęp") || strings.Contains(lower, "folder is busy") || strings.Contains(lower, "zajęty")
}

// thunderbirdMoveResult is the subset of a move answer the host acts on.
type thunderbirdMoveResult struct {
	Status               string          `json:"status"`
	MovedButUnverified   json.RawMessage `json:"movedButUnverified"`
	MissingInDestination []string        `json:"missingInDestination"`
	RecheckAfterMs       *int64          `json:"recheckAfterMs"`
	Recheck              *struct {
		Op   string          `json:"op"`
		Args json.RawMessage `json:"args"`
	} `json:"recheck"`
}

// thunderbirdMoveFollowUp tells the model what to do after an IMAP move: Gmail
// can drop a label minutes after the bridge's own settle window, so the bridge
// returns a ready verify_move request to send after recheckAfterMs.
func thunderbirdMoveFollowUp(data json.RawMessage, now time.Time) string {
	var res thunderbirdMoveResult
	if json.Unmarshal(data, &res) != nil {
		return ""
	}
	var notes []string
	if res.Status == "moved_but_unverified" {
		missing := ""
		if len(res.MissingInDestination) > 0 {
			missing = fmt.Sprintf(" (%d Message-ID(s) not yet seen in the destination)", len(res.MissingInDestination))
		}
		notes = append(notes, "status moved_but_unverified"+missing+": do not report the move as done and do not continue with further batches; check the destination and All Mail first (verify_move reports where the messages are).")
	}
	if res.Recheck != nil && res.Recheck.Op == "verify_move" && len(res.Recheck.Args) > 0 {
		wait := time.Duration(0)
		if res.RecheckAfterMs != nil && *res.RecheckAfterMs > 0 {
			wait = time.Duration(*res.RecheckAfterMs) * time.Millisecond
		}
		args := compactJSON(res.Recheck.Args)
		when := "now"
		if wait > 0 {
			when = fmt.Sprintf("after %s (not before %s)", wait.Round(time.Second), now.Add(wait).Format("15:04:05"))
		}
		notes = append(notes, fmt.Sprintf("Gmail/IMAP can still drop a label after the move. Re-check %s with thunderbird_mail op \"verify_move\" and these arguments: %s. It is read-only and needs no confirmation; if it reports moved_but_unverified or onlyInAllMail, tell the user where the messages are.", when, args))
	}
	if len(notes) == 0 {
		return ""
	}
	return "\n\nHost note: " + strings.Join(notes, " ")
}

func compactJSON(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false) // keep <Message-ID> brackets readable
	if enc.Encode(v) != nil {
		return string(raw)
	}
	return strings.TrimSpace(out.String())
}
