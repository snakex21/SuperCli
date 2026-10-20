package interactive

import (
	"context"
	"fmt"
)

// ConfirmAction is a trusted UI gate, independent of model-generated arguments.
// Only the exact UI choice allows one invocation; free-form text never approves.
// With no interactive UI, it fails closed rather than treating silence as yes.
func ConfirmAction(ctx context.Context, fallback chan<- AskRequest, question string) error {
	return ConfirmActionLocalized(ctx, fallback, question, "", nil)
}

// ConfirmActionLocalized adds trusted, static UI localization metadata without
// changing the exact Cancel/Allow once approval gate. Model ask_user arguments
// cannot populate this metadata.
func ConfirmActionLocalized(ctx context.Context, fallback chan<- AskRequest, question, key string, args map[string]string) error {
	trustedArgs := make(map[string]string, len(args))
	for k, v := range args {
		trustedArgs[k] = v
	}
	in := fallback
	if current, ok := ctx.Value(askChannelKey{}).(chan<- AskRequest); ok {
		in = current
	}
	if in == nil {
		return fmt.Errorf("action requires confirmation in an interactive GUI or TUI")
	}
	done := make(chan struct{})
	defer close(done)
	response := make(chan AskAnswer, 1)
	request := AskRequest{ID: newAskID(), Header: "Confirm action", Question: question, QuestionKey: key, QuestionArgs: trustedArgs, Confirmation: true, Options: []AskOption{{Label: "Cancel"}, {Label: "Allow once"}}, Respond: response, Done: done}
	select {
	case in <- request:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case answer := <-response:
		if !answer.Cancelled && len(answer.Selected) == 1 && answer.Selected[0] == "Allow once" && answer.Custom == "" {
			return nil
		}
		return fmt.Errorf("action was not approved")
	case <-ctx.Done():
		return ctx.Err()
	}
}
