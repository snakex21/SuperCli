package tui

import (
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/storage/session"
	"supercli/internal/system/stats"
)

func TestResumeRestoresFullTranscriptAndPreservesDraft(t *testing.T) {
	store, err := session.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	old, err := store.Create(t.TempDir(), "qwen", "Full history")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []llm.Message{
		{Role: llm.RoleSystem, Content: "hidden system"},
		{Role: llm.RoleUser, Content: "first question"},
		{Role: llm.RoleAssistant, Content: "<thinking>careful thought</thinking>**Final answer**", ToolCalls: []llm.ToolCall{{ID: "tool-1", Name: "read_lines", Arguments: "{}"}}},
		{Role: llm.RoleTool, ToolCallID: "tool-1", Content: "file contents"},
		{Role: llm.RoleUser, Content: "last question"},
	} {
		enc, e := session.FromMessage(message)
		if e != nil {
			t.Fatal(e)
		}
		if e = store.AppendMessage(context.Background(), old.ID, enc); e != nil {
			t.Fatal(e)
		}
	}
	m := New(Options{Home: old.Cwd, SessionStore: store, NoColor: true, Language: "pl", Commands: map[string]SlashHandler{"resume": func(context.Context, string) (string, error) { return "raw tail must not leak", nil }}})
	m.width, m.height = 70, 25
	m.input.SetValue("draft stays")
	m.chat.addSystem("old cost output")
	next, cmd := m.resumeConversation(old.ID)
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	got := ansi.Strip(m.chat.render(m.palette))
	for _, want := range []string{"Ty", "first question", "SuperCli", "Myślenie", "careful thought", "Odpowiedź", "Final answer", "read_lines", "last question"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	for _, bad := range []string{"old cost output", "raw tail", "hidden system", "<thinking>", "**Final"} {
		if strings.Contains(got, bad) {
			t.Fatalf("leaked %q in %s", bad, got)
		}
	}
	if m.input.Value() != "draft stays" || m.busy || !m.viewport.AtBottom() {
		t.Fatalf("draft/busy/scroll state: %q %v %v", m.input.Value(), m.busy, m.viewport.AtBottom())
	}
}
func TestResumeFailureDoesNotReplaceChat(t *testing.T) {
	store, _ := session.OpenStore(t.TempDir())
	defer store.Close()
	old, _ := store.Create(t.TempDir(), "model", "History")
	enc, _ := session.FromMessage(llm.Message{Role: llm.RoleUser, Content: "previous"})
	if err := store.AppendMessage(context.Background(), old.ID, enc); err != nil {
		t.Fatal(err)
	}
	m := New(Options{SessionStore: store, Commands: map[string]SlashHandler{"resume": func(context.Context, string) (string, error) { return "", errors.New("load failed") }}})
	m.chat.addUser("keep me")
	next, cmd := m.resumeConversation(old.ID)
	next, _ = next.(Model).Update(cmd())
	m = next.(Model)
	if !strings.Contains(m.chat.render(m.palette), "keep me") || m.loadedSessionID != "" {
		t.Fatal("failed resume replaced history")
	}
}
func TestUsageIncludesHelpersAndUsesCurrentSession(t *testing.T) {
	rec := stats.NewMemory()
	rec.RecordCall(stats.Call{Purpose: "main", TokensIn: 1000, TokensOut: 100, Model: "space-bunny-free"})
	rec.RecordCall(stats.Call{Purpose: "memory", TokensIn: 649, TokensOut: 191, Model: "space-bunny-free", Background: true})
	m := New(Options{Home: t.TempDir(), DataDir: t.TempDir(), SessionID: "active-id", StatsRecorder: rec, NoColor: true, Language: "pl"})
	m.width, m.height = 96, 30
	m.input.SetValue("unfinished")
	next, cmd := m.openUsageMenu()
	next, _ = next.(Model).Update(cmd())
	m = next.(Model)
	d := m.menu.usage
	if d == nil || d.input != 1649 || d.output != 291 || d.sessionID != "active-id" || d.cost.State != "free" {
		t.Fatalf("usage=%+v", d)
	}
	got := m.View()
	if !strings.Contains(got, "Zużycie i koszty") || !strings.Contains(got, "Bezpłatnie") || strings.Contains(got, "**") {
		t.Fatalf("bad usage view: %s", got)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.input.Value() != "unfinished" {
		t.Fatal("usage discarded draft")
	}
}

type attachmentTestAgent struct {
	sentAddon  string
	sentImages []llm.ImageRef
	addon      string
	images     []llm.ImageRef
	prompt     string
	runs       int
}

func (a *attachmentTestAgent) Name() string                            { return "attachment-test" }
func (a *attachmentTestAgent) SetNextUserAddon(s string)               { a.addon = s }
func (a *attachmentTestAgent) SetNextUserImages(images []llm.ImageRef) { a.images = images }
func (a *attachmentTestAgent) Run(ctx context.Context, prompt string) (<-chan agent.Event, error) {
	a.sentAddon = a.addon
	a.sentImages = append([]llm.ImageRef(nil), a.images...)
	a.prompt = prompt
	a.runs++
	ch := make(chan agent.Event)
	close(ch)
	return ch, nil
}
func TestAttachmentsReachAgentAsPixelsAndDocumentPaths(t *testing.T) {
	home := t.TempDir()
	imagePath := filepath.Join(home, "screen.png")
	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 20, 20))); err != nil {
		t.Fatal(err)
	}
	file.Close()
	doc := filepath.Join(home, "draft.docx")
	os.WriteFile(doc, []byte("document"), 0600)
	a := &attachmentTestAgent{}
	m := New(Options{Home: home, Agent: a, NoColor: true})
	m.pendingAttachments = []string{imagePath, doc}
	next, cmd := m.startPrompt("review these")
	event := cmd().(runStartMsg)
	if event.err != nil {
		t.Fatal(event.err)
	}
	next, _ = next.(Model).Update(event)
	m = next.(Model)
	if a.runs != 1 || len(a.sentImages) != 1 || a.sentImages[0].Data == "" || !strings.Contains(a.sentAddon, "draft.docx") || strings.Contains(a.prompt, "attached_files") {
		t.Fatalf("attachments not prepared: %+v", a)
	}
	if len(m.pendingAttachments) != 0 {
		t.Fatal("sent files left selected")
	}
	if !strings.Contains(m.chat.render(m.palette), "screen.png") {
		t.Fatal("missing visible attachment")
	}
}
func TestInvalidAttachmentKeepsSelectionAndRestoresDraft(t *testing.T) {
	a := &attachmentTestAgent{}
	m := New(Options{Home: t.TempDir(), Agent: a})
	m.pendingAttachments = []string{filepath.Join(m.home, "missing.png")}
	next, cmd := m.startPrompt("my draft")
	next, _ = next.(Model).Update(cmd())
	m = next.(Model)
	if a.runs != 0 || m.busy || len(m.pendingAttachments) != 1 || m.input.Value() != "my draft" {
		t.Fatalf("bad retry state: %+v", m.pendingAttachments)
	}
}
func TestAttachmentPickerSelectionAndRemoval(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "one.txt"), []byte("one"), 0600)
	os.Mkdir(filepath.Join(home, "folder"), 0700)
	m := New(Options{Home: home, NoColor: true})
	m.input.SetValue("keep draft")
	next, cmd := m.openAttachmentsMenu()
	next, _ = next.(Model).Update(cmd())
	m = next.(Model)
	if len(m.attachmentRows()) != 2 || !m.attachmentRows()[0].dir {
		t.Fatal("directories must sort first")
	}
	m.menu.cursor = 3
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if len(m.pendingAttachments) != 1 {
		t.Fatal("not selected")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	m = next.(Model)
	if len(m.pendingAttachments) != 0 {
		t.Fatal("not removed")
	}
	m.menu.cursor = 0
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.mode != modeNormal || m.input.Value() != "keep draft" {
		t.Fatal("picker lost draft")
	}
}
func TestCommandDocumentDoesNotLeakMarkdownOrClearNewDraft(t *testing.T) {
	m := New(Options{NoColor: true})
	m.input.SetValue("typing while command returns")
	next, _ := m.Update(slashResultMsg{Document: true, Body: "## Report\n**bold** and \x60code\x60"})
	m = next.(Model)
	text := m.chat.render(m.palette)
	if strings.Contains(text, "**") || strings.Contains(text, "\x60") || m.input.Value() != "typing while command returns" {
		t.Fatalf("bad command result %q", text)
	}
}

func TestTerminalSymbolFallbackKeepsRawHistory(t *testing.T) {
	for _, tc := range []struct {
		platform, wt, program, override string
		want                            bool
	}{
		{"windows", "", "", "", true}, {"windows", "session", "", "", false}, {"linux", "", "", "", false}, {"windows", "", "", "on", false}, {"linux", "", "", "off", true},
	} {
		if got := legacyTerminalSymbolsFor(tc.platform, tc.wt, tc.program, tc.override); got != tc.want {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
	c := newChat(45)
	c.legacySymbols = true
	raw := "Cześć! 👋 🙂 ✅ Żółć"
	c.addAssistant(raw)
	got := c.render(NoColorPalette())
	if !strings.Contains(got, "o/ :) [OK]") || !strings.Contains(got, "Żółć") || c.lastAssistant() != raw {
		t.Fatalf("fallback changed original or left broken symbols: %q", got)
	}
	c.legacySymbols = false
	c.completedDirty = true
	if !strings.Contains(c.render(NoColorPalette()), "👋") {
		t.Fatal("modern terminal lost emoji")
	}
}
func TestLateLocalCommandDoesNotStopAgent(t *testing.T) {
	m := New(Options{NoColor: true})
	m.busy = true
	canceled := false
	m.cancel.Arm(cancelRun, func() { canceled = true })
	next, _ := m.Update(slashResultMsg{Local: true, Document: true, Body: "**Local result**"})
	m = next.(Model)
	if !m.busy || canceled {
		t.Fatal("late local result changed running agent")
	}
}
func TestUsageAndAttachmentMenusFitTerminal(t *testing.T) {
	for _, width := range []int{45, 62, 100, 150} {
		for _, language := range []string{"pl", "en"} {
			m := New(Options{NoColor: true, Language: language})
			m.width, m.height = width, 26
			m.mode = modeMenu
			m.menu = interactiveMenu{kind: menuUsage, usage: &usageSnapshot{sessionID: "active", model: "qwen", input: 4000, output: 200}}
			for _, kind := range []menuKind{menuUsage, menuAttachments} {
				m.menu.kind = kind
				view := m.View()
				if len(strings.Split(view, "\n")) > m.height {
					t.Fatalf("%d %s height overflow", width, language)
				}
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > width {
						t.Fatalf("%d %s width overflow: %q", width, language, line)
					}
				}
			}
		}
	}
}
