package agent

import (
	"context"
	"errors"
	"supercli/internal/llm"
	"testing"
	"time"
)

func TestConsumeCancellationDoesNotWaitForProducerToClose(t *testing.T) {
	for _, reasoning := range []bool{false, true} {
		t.Run(map[bool]string{false: "answer", true: "reasoning"}[reasoning], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			input := make(chan llm.Delta)
			defer close(input)
			output := make(chan Event)
			type result struct {
				text  string
				usage *llm.Usage
				err   error
			}
			finished := make(chan result, 1)
			go func() {
				text, _, usage, err := (&Loop{}).consume(ctx, input, output)
				finished <- result{text, usage, err}
			}()
			if reasoning {
				input <- llm.Delta{Reasoning: "partial thought"}
			} else {
				input <- llm.Delta{Content: "partial answer"}
			}
			<-output // accepted content; the producer deliberately stays open
			input <- llm.Delta{Usage: &llm.Usage{Input: 100, Output: 3}}
			input <- llm.Delta{Notice: "barrier"}
			<-output // usage received, then blocked waiting for another delta
			cancel()
			select {
			case got := <-finished:
				if !errors.Is(got.err, context.Canceled) {
					t.Fatalf("err=%v", got.err)
				}
				want := "partial answer"
				if reasoning {
					want = "<thinking>partial thought</thinking>\n"
				}
				if got.text != want || got.usage == nil || got.usage.Input != 100 || got.usage.Output != 3 {
					t.Fatalf("lost accepted content: %+v", got)
				}
			case <-time.After(time.Second):
				t.Fatal("Stop waited for the still-open provider channel")
			}
		})
	}
}

func TestConsumeCanceledClosedStreamReturnsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input := make(chan llm.Delta)
	close(input)
	_, _, _, err := (&Loop{}).consume(ctx, input, make(chan Event))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
