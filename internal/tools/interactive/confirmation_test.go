package interactive

import (
	"context"
	"errors"
	"testing"
)

func TestConfirmActionRequiresUIAndExactChoice(t *testing.T) {
	if ConfirmAction(context.Background(), nil, "action") == nil {
		t.Fatal("headless approval")
	}
	for _, answer := range []AskAnswer{{Selected: []string{"Allow once"}}, {Cancelled: true, Selected: []string{"Allow once"}}, {Custom: "Allow once"}, {Selected: []string{"Allow once", "Cancel"}}, {Selected: []string{"Cancel"}}} {
		ch := make(chan AskRequest, 1)
		done := make(chan error, 1)
		go func() { done <- ConfirmAction(context.Background(), ch, "desktop click at 10,20") }()
		req := <-ch
		if !req.Confirmation || req.Options[0].Label != "Cancel" || req.AllowCustom || req.Question != "desktop click at 10,20" {
			t.Fatal("invalid consent UI")
		}
		req.Respond <- answer
		err := <-done
		want := !answer.Cancelled && len(answer.Selected) == 1 && answer.Selected[0] == "Allow once" && answer.Custom == ""
		if (err == nil) != want {
			t.Fatalf("answer=%+v err=%v", answer, err)
		}
		select {
		case <-req.Done:
		default:
			t.Fatal("question not closed")
		}
	}
}
func TestConfirmActionCancellationAndContextChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan AskRequest, 1)
	done := make(chan error, 1)
	go func() { done <- ConfirmAction(WithAskChannel(ctx, ch), nil, "cancel action") }()
	req := <-ch
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-req.Done:
	default:
		t.Fatal("question not closed")
	}
}
