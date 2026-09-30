package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"supercli/internal/llm"
	"supercli/internal/tools"
)

func TestLazyPreflightSkipsRepeatedGreetingAndCollectsOnceForProject(t *testing.T) {
	provider := &capturingProvider{reply: "done"}
	writer := &recordingWriter{}
	loop, err := NewLoop(LoopConfig{
		Provider: provider, Registry: tools.NewRegistry(), System: "stable-prefix",
		Writer: writer, EnableNavigator: true, NavigatorAuto: true, NavigatorKeywordsOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	collections := 0
	loop.SetNextCoordinatorAddonSource(func(context.Context) string {
		collections++
		return testRepoBlock
	})
	for _, prompt := range []string{"cześć", "cześć", "inspect project files", "fix another file"} {
		ch, err := loop.Run(context.Background(), prompt)
		if err != nil {
			t.Fatal(err)
		}
		drainEvents(t, ch)
		if prompt == "cześć" && collections != 0 {
			t.Fatal("greeting collected repository context")
		}
	}
	if collections != 1 {
		t.Fatalf("collections=%d, want one project collection", collections)
	}
	requests := provider.requests()
	if len(requests) != 4 {
		t.Fatalf("provider requests=%d, want one per prompt", len(requests))
	}
	for i, request := range requests {
		found := false
		for _, message := range request {
			hasBlock := strings.Contains(message.TextOnly().Content, testRepoBlock)
			if message.Role == llm.RoleSystem && hasBlock {
				t.Fatal("repo context changed system prefix")
			}
			if hasBlock {
				found = true
			}
		}
		if found != (i >= 2) {
			t.Fatalf("request %d repo context present=%v", i, found)
		}
	}
	for _, message := range writer.messages {
		if message.Role == llm.RoleUser && strings.Contains(message.Content, testRepoBlock) {
			t.Fatal("internal addon leaked into user transcript")
		}
	}
}

func TestLazyPreflightCancellationDoesNotBlockAcceptanceOrConsumeSource(t *testing.T) {
	provider := &capturingProvider{reply: "done"}
	loop := makeLoop(t, provider, tools.NewRegistry(), "SYS")
	started := make(chan struct{})
	collections := 0
	loop.SetNextCoordinatorAddonSource(func(ctx context.Context) string {
		collections++
		if collections == 1 {
			close(started)
			<-ctx.Done()
		}
		return testRepoBlock
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	accepted := make(chan (<-chan Event), 1)
	go func() {
		ch, err := loop.Run(ctx, "inspect project files")
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- ch
	}()
	var events <-chan Event
	select {
	case events = <-accepted:
		if events == nil {
			t.Fatal("run was not accepted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("repository collection blocked Run acceptance")
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not begin lazy collection")
	}
	cancel()
	for range events {
	}
	if len(provider.requests()) != 0 {
		t.Fatal("canceled collection reached provider")
	}
	ch, err := loop.Run(context.Background(), "inspect project files")
	if err != nil {
		t.Fatal(err)
	}
	drainEvents(t, ch)
	if collections != 2 {
		t.Fatalf("canceled source not retried: %d", collections)
	}
	requests := provider.requests()
	if len(requests) != 1 {
		t.Fatalf("provider requests=%d after retry", len(requests))
	}
	found := false
	for _, message := range requests[0] {
		if message.Role == llm.RoleUser && strings.Contains(message.Content, testRepoBlock) {
			found = true
		}
	}
	if !found {
		t.Fatal("retry lost repository addon")
	}
}

func TestLazyPreflightEmptyResultDoesNotRepeatFilesystemWork(t *testing.T) {
	loop := makeLoop(t, &capturingProvider{reply: "done"}, tools.NewRegistry(), "SYS")
	collections := 0
	loop.SetNextCoordinatorAddonSource(func(context.Context) string { collections++; return "" })
	for range 2 {
		ch, err := loop.Run(context.Background(), "inspect project files")
		if err != nil {
			t.Fatal(err)
		}
		drainEvents(t, ch)
	}
	if collections != 1 {
		t.Fatalf("empty collection repeated: %d", collections)
	}
}
