// Package tui is the Bubble Tea presentation layer. F25 replaces
// the raw transcript with a structured chat view (role-based
// colors), adds a status bar, inline event markers, a tool-
// name spinner, Ctrl+C run cancellation, and PgUp/PgDn scrolling.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"supercli/internal/account/cost"
	"supercli/internal/agent"
	"supercli/internal/agent/planmode"
	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/system/doctor"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
	"supercli/internal/tools/fileops"
	"supercli/internal/tools/mentions"
	"supercli/internal/tools/shellescape"
	"supercli/internal/ui/export"
)

// mode is the input/interaction mode. The TUI flips between them
// when an AskRequest comes in and when the user answers.
type mode int

const (
	modeNormal mode = iota
	modeAsking
	modeMenu
	modeDoctor
)

// Model is the root Bubble Tea model. F25 adds Palette, Marker,
// structured chat, CancelState, and ScrollConfig.
type Model struct {
	home    string
	dataDir string

	// version and tierName feed the slim header bar
	// ("✻ SuperCli 0.6.0 · model · tier").
	version  string
	tierName string
	agent    agent.Agent
	llm      llm.Provider

	// commands is a map of slash command name -> handler.
	// Set via New and used by handleKey to dispatch
	// `/`-prefixed input. nil = no slash commands.
	commands map[string]SlashHandler

	// statusFn returns the footer status line. nil
	// disables the status line entirely. F7.
	statusFn func() string

	// F25: theme and styled markers
	palette Palette
	marker  Marker

	// Widgets
	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model

	// State
	quitting bool
	busy     bool
	mode     mode
	width    int
	height   int

	// F25: structured chat (replaces raw transcript).
	chat chat

	// F25: Ctrl+C cancellation state.
	cancel CancelState

	// F25: scroll config for PgUp/PgDn.
	scroll ScrollConfig

	// AskUser state. Set when mode == modeAsking; nil otherwise.
	pendingAsk *pendingAsk

	// F26.2: shellRunner handles "!command" shell escapes.
	// nil = shell escapes disabled.
	shellRunner *shellescape.Runner

	// F26.3: planMode toggles read-only analysis mode.
	planMode bool

	// F26.4: tracker records file changes for /diff.
	tracker *fileops.Tracker

	// F26.5: modelSwapper for /model hot-swap.
	modelSwapper  ModelSwapper
	modelLister   ModelLister
	modelSwapFn   ModelSwapFunc
	sessionStore  *session.Store
	statsRecorder stats.Recorder     // F28: per-turn metrics for /cost
	providerMgr   *providers.Manager // F30: provider management
	caps          *llm.CapabilityRegistry
	goalSvc       *goal.Service
	toolRegistry  *tools.Registry
	doctorReport  *doctor.Report
	menu          interactiveMenu
	autocomp      autocomplete // autocomplete popup state
	// providerStatuses caches async connectivity probe results for
	// the /providers menu (key: provider name). The menu renders
	// instantly with "checking..." and statuses pop in as the
	// background pings finish.
	providerStatuses map[string]providerStatus

	// onRunEnd is invoked (in a goroutine) after each agent run
	// finishes. See Options.OnRunEnd.
	onRunEnd func()

	// statusOverride holds a temporary status message (e.g.
	// "cancelled") that replaces the normal status bar for
	// a few seconds. Cleared by statusOverrideClearMsg.
	statusOverride string

	// extCh is the channel the loop emits non-Run events on
	// (F12 ConsultEvent). nil = no external sink.
	extCh <-chan agent.Event

	// eventCh is the channel for the active Run's events.
	// Set on runStartMsg, cleared on runEndMsg / ErrorEvent /
	// slashResultMsg / shellResultMsg. waitForNextEvent()
	// reads from it.
	eventCh <-chan agent.Event

	// Streaming accumulation. The latest assistant text
	// chunk is appended here and rendered in-place until
	// DoneEvent flushes it into chat.
	current string

	// responseLen tracks the total character count of the
	// current assistant response. Used for chars/4 token
	// estimation when the provider doesn't report usage.
	responseLen int

	// lastToolName stores the name of the most recent tool
	// call. Used to label the tool result in the chat.
	lastToolName string

	// toolExpanded: when true, tool results are shown in
	// full (max 50 lines). Toggled with 'E' key.
	toolExpanded bool

	// tipShown guards the "use Ctrl+C or /quit to exit" hint so
	// it is printed at most once per session (it used to be
	// appended on every empty-input Esc/Enter, spamming the
	// transcript).
	tipShown bool

	// Transcript holds the raw text for backward-compatible
	// test assertions. Do NOT use strings.Builder here:
	// Bubble Tea models are copied by value, and Builder panics
	// after a non-zero copy. transcriptBuffer is copy-safe: it
	// holds a pointer to its backing slice, so every copy of the
	// model shares the same buffer and appends are amortized O(1)
	// (the old string-concatenation version was O(n²)).
	transcript transcriptBuffer
}

// transcriptBuffer is a copy-safe, append-only text buffer.
// The backing []byte lives behind a pointer: copying the
// struct (and hence the Bubble Tea model) copies only the
// pointer, so all copies append to the same buffer and no
// copy can observe a stale slice header. The zero value is
// ready to use; the backing slice is allocated lazily on
// first write.
type transcriptBuffer struct{ buf *[]byte }

func (t *transcriptBuffer) ensure() {
	if t.buf == nil {
		t.buf = new([]byte)
	}
}

// WriteString appends s. It implements io.StringWriter.
func (t *transcriptBuffer) WriteString(s string) (int, error) {
	t.ensure()
	*t.buf = append(*t.buf, s...)
	return len(s), nil
}

// WriteByte appends b. It implements io.ByteWriter.
func (t *transcriptBuffer) WriteByte(b byte) error {
	t.ensure()
	*t.buf = append(*t.buf, b)
	return nil
}

func (t transcriptBuffer) String() string {
	if t.buf == nil {
		return ""
	}
	return string(*t.buf)
}

// SlashHandler is the signature for a TUI slash command
// (e.g. `/darwin 3 fix bug`). The handler runs in a
// goroutine; it must respect ctx for cancellation.
type SlashHandler func(ctx context.Context, args string) (string, error)

// pendingAsk is the live state of an active ask_user interaction.
type pendingAsk struct {
	Question    string
	Header      string
	Options     []tools.AskOption
	MultiSelect bool
	// cursor is the currently focused option (0..len(Options)-1).
	cursor int
	// toggled tracks which options are checked in multi-select
	// mode. Only meaningful when MultiSelect is true.
	toggled map[int]bool
	// respond is the channel back to the tool's goroutine.
	respond chan tools.AskAnswer
}

// Options bundles optional dependencies so the constructor signature
// stays stable as the real loop grows.
type Options struct {
	Home     string
	DataDir  string
	Agent    agent.Agent
	LLM      llm.Provider
	Commands map[string]SlashHandler
	// StatusFn, if non-nil, is called from View() to
	// render the footer status line (typically
	// "credits: 1.2k/10k (12%)"). The TUI does not own
	// the credit state; main.go does.
	StatusFn func() string
	// ExtCh is the loop's external event channel
	// (F12 ConsultEvent). When non-nil, the TUI
	// starts a read pump in Init() and routes
	// every event through handleAgentEvent so
	// markers like [council: ...] land in the
	// transcript. nil = no external events.
	ExtCh <-chan agent.Event
	// NoColor disables ANSI color codes. When true
	// the palette is replaced with NoColorPalette.
	NoColor bool
	// ShellRunner handles "!command" shell escapes. nil = disabled.
	ShellRunner *shellescape.Runner
	// F26.4: Tracker records file changes for /diff.
	Tracker *fileops.Tracker
	// F26.5: ModelSwapper allows /model to hot-swap providers.
	// nil = /model disabled.
	ModelSwapper ModelSwapper
	// F26.5: ModelLister provides the list of available models.
	// nil = /model shows current only.
	ModelLister ModelLister
	// F26.5: ModelSwapFn builds a new provider for the target model.
	// nil = swap disabled.
	ModelSwapFn ModelSwapFunc
	// F26.6: SessionStore for /resume.
	SessionStore *session.Store
	// F28: StatsRecorder provides per-turn metrics for /cost dashboard.
	StatsRecorder stats.Recorder
	// F30: ProviderMgr manages the provider list from config.toml.
	ProviderMgr *providers.Manager
	// CapabilityRegistry feeds /models and /providers menus.
	CapabilityRegistry *llm.CapabilityRegistry
	// GoalService feeds the interactive /goal task menu.
	GoalService *goal.Service
	// ToolRegistry feeds /doctor diagnostics.
	ToolRegistry *tools.Registry
	// OnRunEnd, if non-nil, is called in a goroutine after each
	// agent run finishes (runEndMsg). main.go uses it for the
	// incremental memory saver so exits are instant.
	OnRunEnd func()
	// Version is shown in the header bar (e.g. "0.6.0").
	Version string
	// Tier is the active model tier shown in the header
	// (e.g. "big", "small"). Optional.
	Tier string
}

// ModelSwapper is implemented by agent.Loop for /model hot-swap.
type ModelSwapper interface {
	CurrentModel() string
	SetModel(p llm.Provider)
}

// ModelLister provides the list of available models.
type ModelLister interface {
	ListModels() []llm.ModelInfo
}

// ModelSwapFunc is called by /model to perform the actual swap.
// It receives the target model ID and provider name, returns the new provider.
type ModelSwapFunc func(modelID, provider string) (llm.Provider, error)

// New builds the root model.
func New(opts Options) Model {
	ti := newInputArea()
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Points

	vp := viewport.New(80, 20)

	// F25: build the palette. NoColor → plain text.
	p := DefaultPalette()
	if opts.NoColor {
		p = NoColorPalette()
	}
	mkr := NewMarker(p)
	sp.Style = p.Header // accent-colored spinner animation
	ti.FocusedStyle.Prompt = p.InputPrompt
	ti.FocusedStyle.Text = p.InputText
	ti.FocusedStyle.Placeholder = p.InputHint
	ti.FocusedStyle.CursorLine = p.InputText
	ti.BlurredStyle.Prompt = p.InputPrompt
	ti.BlurredStyle.Text = p.InputText
	ti.BlurredStyle.Placeholder = p.InputHint

	vp.SetContent(welcome(opts, p))

	return Model{
		home:          opts.Home,
		dataDir:       opts.DataDir,
		version:       opts.Version,
		tierName:      opts.Tier,
		agent:         opts.Agent,
		llm:           opts.LLM,
		commands:      opts.Commands,
		statusFn:      opts.StatusFn,
		palette:       p,
		marker:        mkr,
		extCh:         opts.ExtCh,
		viewport:      vp,
		input:         ti,
		spinner:       sp,
		chat:          newChat(80),
		cancel:        NewCancelState(),
		scroll:        ScrollConfig{},
		shellRunner:   opts.ShellRunner,
		tracker:       opts.Tracker,
		modelSwapper:  opts.ModelSwapper,
		modelLister:   opts.ModelLister,
		modelSwapFn:   opts.ModelSwapFn,
		sessionStore:  opts.SessionStore,
		statsRecorder: opts.StatsRecorder,
		providerMgr:   opts.ProviderMgr,
		caps:          opts.CapabilityRegistry,
		goalSvc:       opts.GoalService,
		toolRegistry:  opts.ToolRegistry,
		onRunEnd:      opts.OnRunEnd,
	}
}

// maxInputLines caps how tall the multi-line input box
// grows; beyond this the textarea scrolls internally.
const maxInputLines = 5

// newInputArea builds the multi-line input widget with the
// project's conventions: no line numbers, 1-line tall until
// the content grows, Enter reserved for "send" (newline is
// Alt+Enter / Ctrl+J — Shift+Enter is indistinguishable
// from Enter on Windows terminals).
func newInputArea() textarea.Model {
	ti := textarea.New()
	ti.Placeholder = "Message SuperCli, or type /help"
	ti.Prompt = "❯ "
	ti.CharLimit = 0
	ti.ShowLineNumbers = false
	ti.MaxHeight = maxInputLines
	ti.SetHeight(1)
	// Enter is intercepted by handleKey to send the message;
	// rebind newline insertion to Alt+Enter / Ctrl+J.
	ti.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "ctrl+j"),
		key.WithHelp("alt+enter", "insert newline"),
	)
	return ti
}

// syncInputHeight grows/shrinks the input box with its
// content, clamped to [1, maxInputLines].
func (m *Model) syncInputHeight() {
	h := m.input.LineCount()
	if h < 1 {
		h = 1
	}
	if h > maxInputLines {
		h = maxInputLines
	}
	if m.input.Height() != h {
		m.input.SetHeight(h)
	}
	m.viewport.Height = m.viewportHeight()
}

// Init satisfies tea.Model. The spinner ticks while the agent
// is running; the text input cursor blinks.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, textarea.Blink}
	// Start the external-sink pump (F12 ConsultEvent).
	if m.extCh != nil {
		cmds = append(cmds, waitForExternalEvent(m.extCh))
	}
	return tea.Batch(cmds...)
}

// Messages exchanged between Update and the agent goroutine.
type runStartMsg struct {
	ch <-chan agent.Event
	// err is set when agent.Run failed before producing a
	// channel; the run never started.
	err error
	// mentionCount/mentionTokens describe @file mentions that
	// were resolved in the background before the run started.
	mentionCount  int
	mentionTokens int
}

type runEventMsg struct {
	ev agent.Event
}

type runEndMsg struct{}

// statusOverrideClearMsg clears the temporary status override
// (e.g. "cancelled" → normal status).
type statusOverrideClearMsg struct{}

// streamFlushMsg forces a View() render during active streaming.
// Emitted by streamFlushCmd every 16ms while m.eventCh != nil.
type streamFlushMsg struct{}

// runExtEventMsg wraps an event from the loop's external sink.
type runExtEventMsg struct {
	ev agent.Event
}

// slashResultMsg is delivered to the TUI when a slash
// command handler returns.
type slashResultMsg struct {
	Body string
	Err  error
}

// askRequestMsg is delivered to the TUI by main.go's pump
// goroutine when an AskRequest arrives on the tool's input channel.
type askRequestMsg struct {
	req tools.AskRequest
}

// AskRequestMsg is the public form of askRequestMsg.
type AskRequestMsg = askRequestMsg

// AskRequestMsgFrom constructs a tea.Msg for the pump goroutine.
func AskRequestMsgFrom(req tools.AskRequest) tea.Msg {
	return askRequestMsg{req: req}
}

// statusRefreshMsg forces Bubble Tea to call View() again so the
// footer status line (statusFn) re-renders. It carries no state:
// the status line is pull-based, so a bare re-render is enough to
// surface data that arrived from a background goroutine (e.g. the
// Codex usage snapshot fetched asynchronously after a model swap).
type statusRefreshMsg struct{}

// StatusRefreshMsg is the public form of statusRefreshMsg, sent by
// main.go via program.Send when a background update (such as the
// Codex rate-limit fetch) should appear in the HUD without the user
// pressing a key.
type StatusRefreshMsg = statusRefreshMsg

// StatusRefreshMsgValue constructs the redraw message for program.Send.
func StatusRefreshMsgValue() tea.Msg { return statusRefreshMsg{} }

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = m.viewportHeight()
		m.chat.width = msg.Width
		m.input.SetWidth(msg.Width)
		return m, nil

	case tea.KeyMsg:
		// Ignore Alt shortcuts so Alt+A/Alt+K do not activate menu
		// actions or insert stray ASCII. Keep non-ASCII AltGr input
		// (Polish chars like ą/ć/ł/ń/ó/ś/ż/ź) working.
		if shouldIgnoreAltKey(msg) {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			return m.handleCtrlC()
		}
		if m.mode == modeAsking {
			return m.handleAskKey(msg)
		}
		if m.mode == modeDoctor {
			return m.handleDoctorKey(msg)
		}
		if m.mode == modeMenu {
			return m.handleMenuKey(msg)
		}
		if m.busy {
			// ESC: soft-cancel current run (not the program).
			if msg.String() == "esc" {
				return m.handleEscCancel()
			}
			// T (Shift+T): toggle thinking block visibility.
			if msg.String() == "T" {
				m.chat.toggleThinking()
				m.refreshTranscript()
				return m, nil
			}
			// E (Shift+E): toggle tool result expansion.
			if msg.String() == "E" {
				m.toolExpanded = !m.toolExpanded
				m.refreshTranscript()
				return m, nil
			}
			// F25: scroll keys work even while busy.
			if HandleScroll(&m.viewport, msg, m.scroll) {
				return m, nil
			}
			return m, nil
		}
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case runStartMsg:
		if msg.err != nil {
			// agent.Run failed before the run started.
			m.cancel.Disarm()
			m.busy = false
			m.appendLine(fmt.Sprintf("(error: %v)", msg.err))
			m.refreshTranscript()
			return m, nil
		}
		if msg.mentionCount > 0 && msg.mentionTokens > 0 {
			m.appendLine(m.marker.Mention(msg.mentionCount, msg.mentionTokens))
			m.refreshTranscript()
		}
		m.busy = true
		m.current = ""
		m.responseLen = 0
		m.eventCh = msg.ch
		return m, tea.Batch(waitForEvent(msg.ch), streamFlushCmd())

	case runEventMsg:
		return m.handleAgentEvent(msg.ev)

	case runExtEventMsg:
		newM, cmd := m.handleAgentEvent(msg.ev)
		if m.extCh != nil {
			cmd = tea.Batch(cmd, waitForExternalEvent(m.extCh))
		}
		return newM, cmd

	case runEndMsg:
		m.busy = false
		m.cancel.Disarm()
		m.eventCh = nil
		if m.onRunEnd != nil {
			go m.onRunEnd()
		}
		m.refreshTranscript()
		m.input.Reset()
		m.syncInputHeight()
		m.input.Focus()
		return m, nil

	case askRequestMsg:
		return m.beginAsk(msg.req)

	case providerStatusMsg:
		if m.providerStatuses == nil {
			m.providerStatuses = make(map[string]providerStatus)
		}
		m.providerStatuses[msg.name] = providerStatus{checked: true, online: msg.online, err: msg.err}
		return m, nil

	case providerSavedMsg:
		if msg.err != nil {
			m.appendLine(m.marker.Error(fmt.Errorf("provider %s: %w", msg.name, msg.err)))
		} else {
			m.appendLine(m.palette.InputHint.Render("provider " + msg.name + ": " + msg.body))
		}
		m.refreshTranscript()
		return m, nil

	case providerScanDoneMsg:
		return m, nil

	case doctorReportMsg:
		m.chat.removeLastSystem(m.marker.Running())
		m.mode = modeDoctor
		m.doctorReport = msg.report
		return m, nil

	case slashResultMsg:
		m.busy = false
		m.cancel.Disarm()
		m.chat.removeLastSystem(m.marker.Running())
		if msg.Err != nil {
			m.appendLine(fmt.Sprintf("_(error: %v)_", msg.Err))
		} else {
			m.appendLine(msg.Body)
		}
		m.refreshTranscript()
		m.input.Reset()
		m.syncInputHeight()
		m.input.Focus()
		return m, nil

	case shellResultMsg:
		m.busy = false
		m.cancel.Disarm()
		m.chat.removeLastSystem(m.marker.Running())
		r := msg.res
		if r.Error != "" {
			m.appendLine(m.marker.Error(fmt.Errorf("%s", r.Error)))
		} else if r.ExitCode != 0 {
			out := r.Stdout
			if r.Stderr != "" {
				out += "\n" + r.Stderr
			}
			m.appendLine(m.marker.ToolResult(
				fmt.Sprintf("!%s (exit %d, %v)\n%s",
					r.Command, r.ExitCode, r.Duration.Round(1e6), out), true))
		} else {
			out := strings.TrimSpace(r.Stdout)
			if out == "" {
				out = "(no output)"
			}
			m.appendLine(m.marker.ToolResult(
				fmt.Sprintf("!%s (%v)\n%s",
					r.Command, r.Duration.Round(1e6), out), false))
		}
		m.refreshTranscript()
		m.input.Reset()
		m.syncInputHeight()
		m.input.Focus()
		return m, nil

	case statusOverrideClearMsg:
		m.statusOverride = ""
		return m, nil

	case statusRefreshMsg:
		// Background data (e.g. an async Codex usage snapshot)
		// arrived. Returning triggers a View() re-render so the
		// pull-based footer picks it up without a keystroke.
		return m, nil

	case streamFlushMsg:
		// Force a View() render during active streaming.
		// The tick stops when eventCh becomes nil (run ends).
		if m.eventCh != nil {
			m.refreshTranscript()
			return m, streamFlushCmd()
		}
		return m, nil

	case modelSwapRequestMsg:
		// F26.5: Perform the actual provider swap. Kept for the
		// /model <name> path (which dispatches this message) and for
		// back-compat; the interactive picker now calls applyModelSwap
		// directly at confirm so apply+save+refresh happen synchronously
		// the instant the user presses Enter (see menuEnter).
		m.applyModelSwap(msg.ModelID, msg.Provider)
		m.refreshTranscript()
		return m, nil
	}
	return m, nil
}

// applyModelSwap performs the model swap and ALL of its side effects
// synchronously and immediately: it rebuilds the provider (via the
// injected modelSwapFn, which also kicks the async Codex usage
// refresh), installs it on the agent loop, updates the header, and
// persists the selection to config.toml so the choice survives even if
// the user closes the CLI right away without sending a message.
//
// It is a no-op-with-notice when swapping is not wired. Callers must
// invoke it only on an actual confirmation — never on picker cancel
// (Esc) — so cancelling never rebuilds the provider or writes config.
func (m *Model) applyModelSwap(modelID, provider string) {
	if m.modelSwapFn == nil || m.modelSwapper == nil {
		m.appendLine(m.marker.ModelInfo(fmt.Sprintf("requested swap to %s", modelID)))
		return
	}
	// modelSwapFn rebuilds the provider AND (for Codex providers)
	// fires kickCodexUsageRefresh in the background, so the HUD
	// limit tile refreshes on its own right after the swap — no
	// message send required.
	newProv, err := m.modelSwapFn(modelID, provider)
	if err != nil {
		m.appendLine(m.marker.Error(fmt.Errorf("model swap failed: %w", err)))
		return
	}
	m.modelSwapper.SetModel(newProv)
	m.llm = newProv // update header display
	// Show the model the user picked, not the provider's internal
	// Name() (a multi-account router reports "router(N providers)",
	// which would leak here). modelID is what the user chose.
	swapLabel := modelID
	if swapLabel == "" {
		swapLabel = newProv.Name()
	}
	m.appendLine(m.marker.ModelInfo(fmt.Sprintf("swapped to %s", swapLabel)))
	// Persist active model + provider for next startup. Done here
	// (at confirm time) rather than lazily at the next send, so
	// closing the CLI immediately after picking keeps the choice.
	if m.providerMgr != nil {
		_ = m.providerMgr.SaveActiveConfig(modelID, provider)
	}
}

// handleCtrlC implements the F25 cancel behavior:
// - If busy: cancel the current agent run (not the process).
// - If asking: cancel the ask.
// - If idle: quit the program. Single-letter keys like q do not quit.
func (m Model) handleCtrlC() (tea.Model, tea.Cmd) {
	if m.busy {
		// Cancel the active run. (This used to append the
		// "running" marker, which read as if the run was
		// still in progress after cancelling.)
		m.cancel.Cancel()
		m.busy = false
		m.cancel.Disarm()
		m.statusOverride = "cancelled"
		m.appendLine(m.palette.InputHint.Render("[Ctrl+C] run cancelled"))
		m.refreshTranscript()
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return statusOverrideClearMsg{}
		})
	}
	if m.mode == modeAsking {
		if m.pendingAsk != nil {
			safeRespond(m.pendingAsk.respond, tools.AskAnswer{Cancelled: true})
		}
		m.endAsk()
		return m, nil
	}
	// Idle → quit.
	m.quitting = true
	return m, tea.Quit
}

// handleEscCancel cancels the current agent run without quitting
// the program. Shows "cancelled" in the status bar for 2 seconds.
// Unlike Ctrl+C (which quits when idle), ESC only acts while busy.
func (m Model) handleEscCancel() (tea.Model, tea.Cmd) {
	if !m.busy {
		return m, nil
	}
	m.cancel.Cancel()
	m.busy = false
	m.cancel.Disarm()
	m.statusOverride = "cancelled"
	m.appendLine(m.palette.InputHint.Render("[ESC] run cancelled"))
	m.refreshTranscript()
	// Clear the override after 2 seconds.
	return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return statusOverrideClearMsg{}
	})
}

// handleKey processes key events when the TUI is idle.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Autocomplete popup navigation — intercept keys BEFORE scroll.
	if m.autocomp.kind != autocompNone {
		return m.handleAutocompleteKey(msg)
	}

	// F25: scroll keys are handled next. When the input box
	// holds multiple lines, arrows/home/end move the cursor
	// inside the textarea instead of scrolling the chat.
	if !(m.input.LineCount() > 1 && isInputNavKey(msg.String())) {
		if HandleScroll(&m.viewport, msg, m.scroll) {
			return m, nil
		}
	}

	switch msg.String() {
	case "esc":
		if m.input.Value() != "" {
			m.input.Reset()
			return m, nil
		}
		// Empty input + Esc is a no-op. The exit tip is shown
		// only once, and only when the user types a bare
		// quit-like word (see the "enter" case below).
		return m, nil
	case "T":
		m.chat.toggleThinking()
		m.refreshTranscript()
		return m, nil
	case "E":
		m.toolExpanded = !m.toolExpanded
		m.refreshTranscript()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		m.input.Reset()
		m.syncInputHeight()
		if text == "" {
			return m, nil
		}
		if isQuitCommand(text) {
			m.quitting = true
			return m, tea.Quit
		}
		// Bare "q"/"quit"/"exit" (no slash): show the exit tip
		// once, then treat further occurrences as regular input.
		if low := strings.ToLower(text); (low == "q" || low == "quit" || low == "exit") && !m.tipShown {
			m.tipShown = true
			m.appendLine(m.palette.InputHint.Render("tip: use Ctrl+C or /quit to exit; q is regular input"))
			m.refreshTranscript()
			return m, nil
		}
		// Slash command? Dispatch to the registered handler.
		if cmd := ParseSlashCommand(text); cmd != nil {
			return m.dispatchSlashCommand(*cmd)
		}
		// F26.2: !command shell escape — run directly without agent.
		if shellescape.IsShellEscape(text) {
			return m.dispatchShellEscape(text)
		}
		if m.agent == nil {
			m.appendLine(m.marker.NoAgent())
			return m, nil
		}
		// Echo the prompt and start the spinner IMMEDIATELY.
		// All potentially blocking work — @mention file reads
		// and agent.Run (which synchronously persists the user
		// message to the SQLite history before returning) —
		// happens inside the tea.Cmd goroutine below, so a slow
		// disk never freezes the input box on Enter.
		//
		// F25: create a cancellable context for Ctrl+C support.
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel.Arm(cancelRun, cancel)
		m.chat.addUser("> " + text)
		m.appendLineToTranscript("> " + text)
		m.busy = true
		m.current = ""
		home, planMode, ag := m.home, m.planMode, m.agent
		return m, func() tea.Msg {
			// F26.1: @file mentions — parse @path references,
			// read files, and prepend content to the prompt.
			prompt := text
			remaining, mentionPaths := mentions.Parse(text)
			var mentionCount, mentionTokens int
			if len(mentionPaths) > 0 {
				ments := mentions.Resolve(home, mentionPaths, 0)
				prompt = mentions.FormatBlock(ments, remaining)
				mentionCount = len(mentionPaths)
				mentionTokens = mentions.TotalTokens(ments)
			}
			// F26.3: if plan mode is active, wrap the prompt.
			runPrompt := prompt
			if planMode {
				runPrompt = planmode.WrapPrompt(prompt)
			}
			ch, err := ag.Run(ctx, runPrompt)
			if err != nil {
				cancel()
				return runStartMsg{err: err}
			}
			return runStartMsg{ch: ch, mentionCount: mentionCount, mentionTokens: mentionTokens}
		}
	case "ctrl+v":
		if text, err := clipboard.ReadAll(); err == nil && text != "" {
			// Multi-line pastes keep their newlines (code,
			// logs, ...). Control chars are still stripped.
			m.input.InsertString(normalizePastedText(text))
			m.syncInputHeight()
			m.updateAutocompleteState()
		}
		return m, nil
	case "ctrl+r":
		// Cycle reasoning effort: off → minimal → low → medium
		// → high → (xhigh on gpt-5.5/codex) → off. Same persistence
		// path as /reasoning (global config.toml).
		modelName := "no-model"
		if m.llm != nil {
			modelName = m.llm.Name()
		}
		if m.llm == nil || !llm.SupportsReasoningEffort(modelName) {
			m.statusOverride = fmt.Sprintf("model %s does not support reasoning effort", modelName)
		} else {
			next := llm.NextReasoningEffort(llm.ReasoningEffort(),
				llm.SupportsXHighReasoningEffort(modelName))
			if err := llm.SetReasoningEffort(next); err != nil {
				m.statusOverride = fmt.Sprintf("reasoning: %v", err)
			} else {
				m.persistReasoningEffort(next)
				if next == "" {
					m.statusOverride = "reasoning: off (provider default)"
				} else {
					m.statusOverride = "reasoning: " + next
				}
			}
		}
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return statusOverrideClearMsg{}
		})
	case "ctrl+y":
		// Copy the last assistant response to the clipboard.
		last := m.chat.lastAssistant()
		if last == "" {
			m.statusOverride = "nothing to copy"
		} else if err := clipboard.WriteAll(last); err != nil {
			m.statusOverride = fmt.Sprintf("copy failed: %v", err)
		} else {
			m.statusOverride = "copied last response"
		}
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return statusOverrideClearMsg{}
		})
	}

	// Pass other keys to the textarea. Alt+Enter / Ctrl+J
	// insert a newline (textarea's rebound InsertNewline);
	// plain Enter never reaches here (handled above).
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.syncInputHeight()

	// After input update, check if autocomplete should activate.
	m.updateAutocompleteState()

	return m, cmd
}

// persistReasoningEffort writes the level to the GLOBAL
// config.toml — the same file the /reasoning slash command
// writes — so Ctrl+R changes survive a restart. Best-effort:
// the in-process level is already set even if the save fails.
func (m *Model) persistReasoningEffort(level string) {
	cwd, _ := os.Getwd()
	globalPath, _ := config.FindTomlPaths(m.dataDir, cwd)
	if tc, err := config.LoadToml(globalPath); err == nil {
		tc.ReasoningEffort = level
		if err := config.SaveToml(globalPath, tc); err != nil {
			m.statusOverride = fmt.Sprintf("reasoning: save config.toml: %v", err)
		}
	}
}

// isInputNavKey reports whether the key is one the multi-line
// input needs for in-box cursor movement.
func isInputNavKey(s string) bool {
	switch s {
	case "up", "down", "home", "end":
		return true
	}
	return false
}

func shouldIgnoreAltKey(msg tea.KeyMsg) bool {
	if !msg.Alt || msg.Paste {
		return false
	}
	// Alt+Enter inserts a newline in the multi-line input.
	if msg.Type == tea.KeyEnter {
		return false
	}
	if len(msg.Runes) == 0 {
		return true
	}
	for _, r := range msg.Runes {
		if r > 127 {
			return false
		}
	}
	return true
}

// normalizePastedText prepares clipboard text for the
// multi-line chat input: newlines are PRESERVED (pasted
// code keeps its formatting), line endings are normalized
// to \n, and control characters are stripped. The Windows
// clipboard is UTF-16 and a bad conversion can leak NUL
// bytes into the pasted text; persisting those corrupts
// config.toml fields such as provider API keys.
func normalizePastedText(text string) string {
	text = strings.TrimRight(text, "\r\n")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	// Drop remaining control characters (NUL, ESC, ...) but
	// keep tabs and newlines.
	text = strings.Map(func(r rune) rune {
		if r != '\t' && r != '\n' && (r < 0x20 || r == 0x7f) {
			return -1
		}
		return r
	}, text)
	return text
}

// normalizePastedLine is the single-line variant used by
// form fields (menu inputs): like normalizePastedText, but
// newlines collapse to single spaces.
func normalizePastedLine(text string) string {
	return strings.ReplaceAll(normalizePastedText(text), "\n", " ")
}

// handleAutocompleteKey handles keys while the autocomplete popup is visible.
func (m Model) handleAutocompleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	filtered := filterItems(m.autocomp.items, m.autocomp.query)

	switch msg.String() {
	case "esc":
		// Close popup, clear input back to just the trigger or empty.
		m.autocomp = autocomplete{}
		m.viewport.Height = m.viewportHeight()
		return m, nil

	case "up", "k":
		if m.autocomp.cursor > 0 {
			m.autocomp.cursor--
		}
		return m, nil

	case "down", "j":
		if m.autocomp.cursor < len(filtered)-1 {
			m.autocomp.cursor++
		}
		return m, nil

	case "enter":
		// Enter in autocomplete: fill AND execute immediately.
		if len(filtered) == 0 {
			m.autocomp = autocomplete{}
			m.viewport.Height = m.viewportHeight()
			return m, nil
		}
		it := filtered[minInt(m.autocomp.cursor, len(filtered)-1)]
		m.autocomp = autocomplete{}
		m.viewport.Height = m.viewportHeight()
		m.input.SetValue(it.Value)
		m.input.CursorEnd()
		// Dispatch the command right away.
		text := strings.TrimSpace(it.Value)
		if cmd := ParseSlashCommand(text); cmd != nil {
			return m.dispatchSlashCommand(*cmd)
		}
		return m, nil

	case "tab":
		// Tab in autocomplete: fill but let user keep typing (add args).
		if len(filtered) == 0 {
			m.autocomp = autocomplete{}
			m.viewport.Height = m.viewportHeight()
			return m, nil
		}
		it := filtered[minInt(m.autocomp.cursor, len(filtered)-1)]
		m.autocomp = autocomplete{}
		m.viewport.Height = m.viewportHeight()
		m.input.SetValue(it.Value)
		m.input.CursorEnd()
		return m, nil

	case "pgup":
		m.autocomp.cursor -= autocompMaxVisible
		if m.autocomp.cursor < 0 {
			m.autocomp.cursor = 0
		}
		return m, nil

	case "pgdown":
		m.autocomp.cursor += autocompMaxVisible
		if m.autocomp.cursor >= len(filtered) {
			m.autocomp.cursor = len(filtered) - 1
		}
		return m, nil
	}

	// For all other keys: update the textinput first, then re-evaluate trigger.
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)

	// If the trigger character is no longer in the input, close the popup.
	kind, query := splitAutocompleteTrigger(m.input.Value())
	if kind == autocompNone {
		m.autocomp = autocomplete{}
		m.viewport.Height = m.viewportHeight()
		return m, cmd
	}

	// Update filter query.
	m.autocomp.query = query
	m.autocomp.cursor = 0
	m.autocomp.scroll = 0

	// If filtered list is empty, close popup.
	if len(filterItems(m.autocomp.items, m.autocomp.query)) == 0 {
		m.autocomp = autocomplete{}
	}
	m.viewport.Height = m.viewportHeight()

	return m, cmd
}

// updateAutocompleteState checks the current input value and opens/closes
// the autocomplete popup accordingly. Called after every textinput update.
func (m *Model) updateAutocompleteState() {
	defer func() {
		m.viewport.Height = m.viewportHeight()
	}()
	text := m.input.Value()
	kind, query := splitAutocompleteTrigger(text)

	if kind == autocompNone {
		if m.autocomp.kind != autocompNone {
			m.autocomp = autocomplete{}
		}
		return
	}

	// Already showing the same kind — just update the filter.
	if m.autocomp.kind == kind {
		m.autocomp.query = query
		m.autocomp.cursor = 0
		m.autocomp.scroll = 0
		if len(filterItems(m.autocomp.items, query)) == 0 {
			m.autocomp = autocomplete{}
		}
		return
	}

	// New trigger — build items and open popup.
	switch kind {
	case autocompSlash:
		m.autocomp = autocomplete{
			kind:  autocompSlash,
			items: buildSlashItems(m.commands),
			query: query,
		}
	case autocompMention:
		m.autocomp = autocomplete{
			kind:  autocompMention,
			items: buildMentionItems(resolveAutocompleteHome(m.home)),
			query: query,
		}
	}

	if len(filterItems(m.autocomp.items, query)) == 0 {
		m.autocomp = autocomplete{}
	}
}

// beginAsk switches the TUI into ask mode.
func (m Model) beginAsk(req tools.AskRequest) (tea.Model, tea.Cmd) {
	if m.mode == modeAsking && m.pendingAsk != nil {
		safeRespond(m.pendingAsk.respond, tools.AskAnswer{Cancelled: true})
	}
	m.pendingAsk = &pendingAsk{
		Question:    req.Question,
		Header:      req.Header,
		Options:     req.Options,
		MultiSelect: req.MultiSelect,
		cursor:      0,
		toggled:     make(map[int]bool),
		respond:     req.Respond,
	}
	m.mode = modeAsking
	m.input.Blur()
	return m, nil
}

// handleAskKey handles key events while mode == modeAsking.
func (m Model) handleAskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := m.pendingAsk
	if a == nil {
		m.mode = modeNormal
		m.input.Focus()
		return m, nil
	}
	switch msg.String() {
	case "esc":
		safeRespond(a.respond, tools.AskAnswer{Cancelled: true})
		m.endAsk()
		return m, nil
	case "enter":
		if a.MultiSelect {
			selected := []string{}
			for i, opt := range a.Options {
				if a.toggled[i] {
					selected = append(selected, opt.Label)
				}
			}
			safeRespond(a.respond, tools.AskAnswer{
				Selected:    selected,
				MultiSelect: true,
			})
		} else {
			idx := a.cursor
			if idx < 0 || idx >= len(a.Options) {
				return m, nil
			}
			safeRespond(a.respond, tools.AskAnswer{
				Selected: []string{a.Options[idx].Label},
			})
		}
		m.endAsk()
		return m, nil
	case "up", "k":
		if a.cursor > 0 {
			a.cursor--
		}
		return m, nil
	case "down", "j":
		if a.cursor < len(a.Options)-1 {
			a.cursor++
		}
		return m, nil
	case " ":
		if a.MultiSelect {
			a.toggled[a.cursor] = !a.toggled[a.cursor]
		}
		return m, nil
	}
	// Quick-pick: 1-4
	if len(msg.Runes) == 1 {
		r := msg.Runes[0]
		if r >= '1' && r <= '4' {
			idx := int(r - '1')
			if idx >= len(a.Options) {
				return m, nil
			}
			if a.MultiSelect {
				a.toggled[idx] = !a.toggled[idx]
				return m, nil
			}
			safeRespond(a.respond, tools.AskAnswer{
				Selected: []string{a.Options[idx].Label},
			})
			m.endAsk()
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) endAsk() {
	m.pendingAsk = nil
	m.mode = modeNormal
	m.input.Focus()
}

// safeRespond sends ans on ch without blocking.
func safeRespond(ch chan<- tools.AskAnswer, ans tools.AskAnswer) {
	select {
	case ch <- ans:
	default:
	}
}

func (m Model) handleAgentEvent(ev agent.Event) (tea.Model, tea.Cmd) {
	switch e := ev.(type) {
	case agent.MessageEvent:
		m.current += e.Text
		m.responseLen += len(e.Text)
		m.chat.appendCurrent(e.Text)
		m.refreshTranscript()
		m.viewport.GotoBottom()
		return m, m.waitForNextEvent()
	case agent.ToolCallEvent:
		m.lastToolName = e.Name
		line := m.marker.ToolCall(e.Name, e.Args)
		m.appendLine(line)
		return m, m.waitForNextEvent()
	case agent.ToolResultEvent:
		if e.Err != nil {
			m.appendLine(m.marker.ToolResultErr(m.lastToolName, e.Err.Error()))
		} else {
			m.appendLine(m.marker.ToolResultFull(m.lastToolName, e.Output, m.toolExpanded))
		}
		return m, m.waitForNextEvent()
	case agent.DoneEvent:
		// Save response text before flush for token estimation.
		responseText := m.current
		m.flushCurrent()
		in, out := e.Usage.Input, e.Usage.Output
		estimated := false
		if in == 0 && out == 0 && len(responseText) > 0 {
			// Provider didn't report usage (e.g. LM Studio).
			// Use tiktoken-go for GPT models, chars/3 for Qwen/Llama,
			// chars/4 as universal fallback.
			modelName := ""
			if m.llm != nil {
				modelName = m.llm.Name()
			}
			out, estimated = llm.CountTokensEstimate(responseText, modelName)
			in = out / 2 // rough input estimate
			if in == 0 {
				in = 1
			}
		}
		m.chat.addSystem(m.marker.DoneEst(in, out, estimated))
		m.appendLineToTranscript(fmt.Sprintf("(done · %d in / %d out)", in, out))
		return m, func() tea.Msg { return runEndMsg{} }
	case agent.ErrorEvent:
		m.flushCurrent()
		err := e.Err
		// A4: a retired/unknown model returns a raw provider
		// error. Surface it as an actionable message instead —
		// and never touch the model registry, so the /model
		// picker keeps working after the failure.
		if isModelUnavailableErr(err) {
			name := "current model"
			if m.llm != nil {
				name = m.llm.Name()
			}
			err = fmt.Errorf("model %q is unavailable on this provider — pick another with /model (%v)", name, e.Err)
		}
		m.chat.addSystem(m.marker.Error(err))
		m.appendLineToTranscript(fmt.Sprintf("(error: %v)", err))
		return m, func() tea.Msg { return runEndMsg{} }
	case agent.DraftUsedEvent:
		line := m.marker.Draft(e.DraftModel, e.VerifierModel, e.Savings, e.Decision)
		m.appendLine(line)
		m.appendLineToTranscript(fmt.Sprintf("[draft: %s → %s, saved %d tokens]", e.DraftModel, e.VerifierModel, e.Savings))
		return m, m.waitForNextEvent()
	case agent.AutoCompactEvent:
		line := fmt.Sprintf("[auto-compact: %d message(s) compacted (%s, ~%d/%d tokens)]",
			e.Removed, e.Reason, e.Estimated, e.Window)
		m.appendLine(line)
		m.appendLineToTranscript(line)
		return m, m.waitForNextEvent()
	case agent.MessagesHiddenEvent:
		line := m.marker.ContextHid(e.Count, e.Reason)
		m.appendLine(line)
		m.appendLineToTranscript(fmt.Sprintf("[context: hid %d message(s)]", e.Count))
		return m, m.waitForNextEvent()
	case agent.ConsultEvent:
		if e.AllFailed {
			m.appendLine(m.marker.CouncilAllFailed())
			return m, m.waitForNextEvent()
		}
		m.appendLine(m.marker.Council(e.CandidateCount, e.WinnerProvider, e.Reason))
		m.appendLine(m.marker.CouncilQuestion(e.Question))
		return m, m.waitForNextEvent()
	case agent.WorkerNotificationEvent:
		line := fmt.Sprintf("[worker %s: %s] %s", e.TaskID, e.Status, e.Summary)
		m.appendLine(line)
		m.appendLineToTranscript(line)
		return m, m.waitForNextEvent()
	}
	return m, m.waitForNextEvent()
}

// refreshTranscript rewrites the viewport with the colored chat
// and the streaming current text with optional spinner.
func (m *Model) refreshTranscript() {
	// Sync the chat's streaming text with the model's current.
	m.chat.current = m.current
	content := m.chat.renderWithSpinner(m.palette, m.spinner.View())
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

func (m *Model) flushCurrent() {
	if m.current != "" {
		m.chat.addAssistant(m.current)
		m.appendLineToTranscript(m.current)
		m.current = ""
	}
}

// appendLine adds a system-level message to the chat and the
// raw transcript for backward-compatible test assertions.
func (m *Model) appendLine(line string) {
	m.chat.addSystem(line)
	m.appendLineToTranscript(line)
}

// appendLineToTranscript writes to the raw transcript only.
// Used for plain-text markers that don't need chat coloring.
func (m *Model) appendLineToTranscript(line string) {
	m.transcript.WriteString(line)
	m.transcript.WriteByte('\n')
}

func (m *Model) completedLines() string {
	return m.transcript.String()
}

// View renders the current state. When the user is being
// asked a question, the ask UI takes over the full screen.
//
// Layout (top to bottom):
//   - chat area (scrollable viewport)
//   - separator line (dim)
//   - status bar (credits, goal, model)
//   - input line ( "> " prompt + text input)
func (m Model) View() string {
	if m.quitting {
		return "SuperCli closed.\n"
	}
	if m.mode == modeAsking && m.pendingAsk != nil {
		return renderAskView(m.pendingAsk, m.width, m.height)
	}
	if m.mode == modeDoctor && m.doctorReport != nil {
		return m.renderDoctorView()
	}
	if m.mode == modeMenu {
		return m.renderMenuView()
	}
	var b strings.Builder

	// 1. Header chrome
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// 2. Chat area (scrollable)
	b.WriteString(m.viewport.View())

	// 3. Separator line
	sep := m.rule()
	b.WriteString("\n")
	b.WriteString(sep)
	b.WriteString("\n")

	// 4. Status bar
	if m.statusOverride != "" {
		fmt.Fprintf(&b, "%s\n", m.palette.Error.Render(m.statusOverride))
	} else if m.busy {
		fmt.Fprintf(&b, "%s %s\n", m.spinner.View(), m.palette.InputHint.Render("working · Ctrl+C interrupt · Esc cancel"))
	}
	if m.statusFn != nil {
		if line := m.statusFn(); line != "" {
			fmt.Fprintf(&b, "%s\n", line)
		}
	}

	// 5. Autocomplete popup (above input line)
	acView := renderAutocomplete(&m.autocomp, m.width, m.palette)
	if acView != "" {
		b.WriteString(acView)
		b.WriteString("\n")
	}

	// 6. Input line + persistent key hints
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(m.renderHintLine())
	return b.String()
}

// renderHeader draws the slim top bar:
//
//	✻ SuperCli 0.6.0 · <model> · <tier>                    <mode>
func (m Model) renderHeader() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	mode := "idle"
	if m.busy {
		mode = "working"
	} else if m.mode == modeAsking {
		mode = "asking"
	}
	model := "no-model"
	if m.llm != nil {
		model = m.llm.Name()
		// Show the active reasoning-effort level next to the
		// model name, but only when the model actually supports
		// the parameter and a level is set ("" = provider
		// default → show nothing). Read at render time, so it
		// refreshes immediately after /reasoning, Ctrl+R, or a
		// model switch.
		if eff := llm.ReasoningEffort(); eff != "" && llm.SupportsReasoningEffort(model) {
			model += " (" + eff + ")"
		}
	}
	name := "✻ SuperCli"
	if m.version != "" {
		name += " " + m.version
	}
	sep := m.palette.StatusSep.Render(" · ")
	left := m.palette.Header.Render(name) + sep + m.palette.HeaderDim.Render(model)
	if m.tierName != "" {
		left += sep + m.palette.HeaderDim.Render(m.tierName)
	}
	right := m.palette.HeaderMode.Render(mode)
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	line := left + strings.Repeat(" ", gap) + right
	if lipgloss.Width(line) > width {
		return truncateVisible(line, width)
	}
	return line
}

// renderInputBox wraps the textarea in a rounded border that
// uses the accent color while focused and a faint border when
// blurred. Visual only — keybindings are unchanged.
func (m Model) renderInputBox() string {
	style := m.palette.InputBorderBlurred
	if m.input.Focused() {
		style = m.palette.InputBorderFocused
	}
	if m.width > 4 {
		style = style.Width(m.width - 2)
	}
	return style.Render(m.input.View())
}

func (m Model) rule() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	return m.palette.Rule.Render(strings.Repeat("─", width))
}

func (m Model) renderHintLine() string {
	hints := []string{"Enter send", "Alt+Enter newline", "Ctrl+Y copy reply", "Ctrl+R change reasoning", "/help commands", "Esc clear", "Ctrl+C interrupt", "PgUp/PgDn scroll", "Shift+T thinking", "Shift+E expand"}
	line := strings.Join(hints, " · ")
	if m.width > 0 && lipgloss.Width(line) > m.width {
		line = "Enter send · Alt+Enter newline · Ctrl+R reasoning · /help · Esc clear · Ctrl+C interrupt"
	}
	return m.palette.InputHint.Render(line)
}

func truncateVisible(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)+"…") > width {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func (m Model) viewportHeight() int {
	if m.height <= 0 {
		return 20
	}
	// Header + separator + input (+2 border rows) + key-hints
	// + at least one status line.
	reserved := 8
	// Multi-line input box: account for the extra rows.
	if ih := m.input.Height(); ih > 1 {
		reserved += ih - 1
	}
	reserved += m.autocompleteHeight()
	if m.busy {
		reserved++
	}
	if m.statusFn != nil {
		if s := m.statusFn(); s != "" {
			reserved += len(strings.Split(s, "\n"))
		}
	}
	h := m.height - reserved
	if h < 3 {
		return 3
	}
	return h
}

func (m Model) autocompleteHeight() int {
	if m.autocomp.kind == autocompNone {
		return 0
	}
	filtered := filterItems(m.autocomp.items, m.autocomp.query)
	if len(filtered) == 0 {
		return 0
	}
	visible := len(filtered)
	if visible > autocompMaxVisible {
		visible = autocompMaxVisible
	}
	// renderAutocomplete uses:
	// title + visible rows + optional detail separator/detail + footer separator/footer + bottom.
	height := 1 + visible + 2 + 1
	selected := filtered[minInt(m.autocomp.cursor, len(filtered)-1)]
	if selected.Desc != "" || selected.Hint != "" {
		height += 2
	}
	return height
}

func welcome(opts Options, p Palette) string {
	model := "not configured"
	if opts.LLM != nil {
		model = opts.LLM.Name()
	}
	left := p.Panel.Width(44).Render(
		p.PanelTitle.Render("✻ SuperCli") + "\n" +
			p.PanelMuted.Render("portable AI coding agent") + "\n\n" +
			p.Bold.Render("Welcome back") + "\n" +
			"Ask for a change, inspect files, or run a plan.\n\n" +
			p.Dim.Render("model ") + p.StatusValue.Render(model),
	)
	right := p.Panel.Width(44).Render(
		p.PanelTitle.Render("Start here") + "\n\n" +
			p.HeaderMode.Render("/help") + p.Dim.Render("      command palette") + "\n" +
			p.HeaderMode.Render("/plan") + p.Dim.Render("      read-only planning") + "\n" +
			p.HeaderMode.Render("/diff") + p.Dim.Render("      review file changes") + "\n" +
			p.HeaderMode.Render("/resume") + p.Dim.Render("    continue session") + "\n\n" +
			p.Dim.Render("Esc clears · Ctrl+C interrupts · Shift+E expands tools"),
	)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right) + "\n"
}

func isQuitCommand(text string) bool {
	s := strings.ToLower(strings.TrimSpace(text))
	return s == "/quit" || s == "/exit"
}

// waitForEvent returns a tea.Cmd that reads one event from ch
// and re-emits it as a tea.Msg.
func waitForEvent(ch <-chan agent.Event) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return runEndMsg{}
		}
		return runEventMsg{ev: ev}
	}
}

// waitForNextEvent returns a tea.Cmd that reads the next event
// from m.eventCh. Returns nil when the run has ended (eventCh is
// nil). This is the correct continuation after a non-terminal
// agent event (MessageEvent, ToolCallEvent, etc.).
func (m *Model) waitForNextEvent() tea.Cmd {
	return waitForEvent(m.eventCh)
}

// streamFlushCmd returns a tea.Cmd that sleeps 16ms then emits
// streamFlushMsg. This forces Bubble Tea to call View() between
// rapid-fire MessageEvents so the user sees text appear live
// (token-by-token streaming) instead of all at once on DoneEvent.
func streamFlushCmd() tea.Cmd {
	return func() tea.Msg {
		time.Sleep(16 * time.Millisecond)
		return streamFlushMsg{}
	}
}

// waitForExternalEvent is the F12 companion for the external sink.
func waitForExternalEvent(ch <-chan agent.Event) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return runExtEventMsg{ev: ev}
	}
}

// nil_to_ctx is a tiny helper. Kept for backward compat with
// any callers that still use it; new code should pass ctx.
func nil_to_ctx() context.Context { return context.Background() }

// dispatchSlashCommand runs a slash command handler in a
// goroutine and returns a tea.Cmd that emits a
// slashResultMsg when the handler is done.
func (m Model) dispatchSlashCommand(cmd SlashCommand) (tea.Model, tea.Cmd) {
	if cmd.Name == "quit" || cmd.Name == "exit" {
		m.quitting = true
		return m, tea.Quit
	}
	// /providers without args opens the interactive menu; with
	// args it falls through to the text subcommand handler below
	// (add/remove/price/toggle), which used to be dead code.
	if cmd.Name == "providers" && cmd.Args == "" {
		return m.openProvidersMenu()
	}
	// Bare /goal opens the interactive task menu; with args it
	// falls through to the text handler (set/list/show/tasks/done),
	// which was previously unreachable dead code.
	if cmd.Name == "goal" && cmd.Args == "" {
		return m.openGoalMenu()
	}
	// F26.3: /plan toggles plan mode (TUI-level, not agent-level).
	if cmd.Name == "plan" {
		m.planMode = !m.planMode
		on := m.planMode
		return m, func() tea.Msg {
			return slashResultMsg{
				Body: m.marker.PlanMode(on),
			}
		}
	}
	// F27: /export saves session to Markdown file.
	if cmd.Name == "export" {
		store := m.sessionStore
		dm := m.marker
		home := m.home
		args := cmd.Args
		return m, func() tea.Msg {
			if store == nil {
				return slashResultMsg{Body: dm.Diff("/export: session store not available")}
			}
			// Get current session ID from the last session.
			sessions, err := store.List(1)
			if err != nil || len(sessions) == 0 {
				return slashResultMsg{Body: dm.Diff("No active session to export.")}
			}
			sess := sessions[0]
			msgs, err := store.ReadMessages(context.Background(), sess.ID)
			if err != nil {
				return slashResultMsg{Err: fmt.Errorf("export read: %w", err)}
			}
			opts := export.Options{
				ID:        sess.ID,
				Title:     sess.Title,
				Model:     sess.Model,
				Cwd:       sess.Cwd,
				CreatedAt: sess.CreatedAt,
				UpdatedAt: sess.UpdatedAt,
				TokensIn:  sess.TokenIn,
				TokensOut: sess.TokenOut,
				Messages:  msgs,
			}
			content := export.RenderMarkdown(opts)
			// /export clip → copy to clipboard instead of a file, so
			// the user can paste the transcript straight out of the
			// alt-screen TUI (where mouse-selection does not work).
			if a := strings.TrimSpace(args); a == "clip" || a == "clipboard" {
				if err := clipboard.WriteAll(content); err != nil {
					return slashResultMsg{Err: fmt.Errorf("export clipboard: %w", err)}
				}
				return slashResultMsg{Body: dm.ModelInfo(fmt.Sprintf("copied %d messages to clipboard", len(msgs)))}
			}
			filename := export.DefaultFilename(opts)
			if args != "" {
				filename = strings.TrimSpace(args)
			}
			path := home + "/" + filename
			if err := writeExportFile(path, content); err != nil {
				return slashResultMsg{Err: fmt.Errorf("export write: %w", err)}
			}
			return slashResultMsg{Body: dm.ModelInfo(fmt.Sprintf("exported %d messages to %s", len(msgs), path))}
		}
	}
	// F26.4: /diff shows file changes from current session.
	if cmd.Name == "diff" {
		tracker := m.tracker
		dm := m.marker
		return m, func() tea.Msg {
			if tracker == nil || tracker.Count() == 0 {
				return slashResultMsg{Body: dm.Diff("No file changes recorded in this session.")}
			}
			return slashResultMsg{Body: dm.Diff(tracker.DiffOutput())}
		}
	}
	// F26.5: /model — without args opens the interactive
	// model picker; with args swaps to the specified model.
	if cmd.Name == "model" {
		if cmd.Args == "" {
			// Open the interactive models menu.
			return m.openModelsMenu()
		}
		// Swap directly by name.
		if m.modelSwapper == nil {
			return m, func() tea.Msg {
				return slashResultMsg{Body: m.marker.Diff("/model not available")}
			}
		}
		if m.modelLister == nil {
			return m, func() tea.Msg {
				return slashResultMsg{Body: m.marker.Diff("/model: listing not available")}
			}
		}
		models := m.modelLister.ListModels()
		target := strings.TrimSpace(cmd.Args)
		var found *llm.ModelInfo
		for i := range models {
			if models[i].ID == target || strings.Contains(models[i].ID, target) {
				found = &models[i]
				break
			}
		}
		if found == nil {
			errMsg := fmt.Sprintf("/model: %q not found in registry", target)
			return m, func() tea.Msg {
				return slashResultMsg{Body: m.marker.Diff(errMsg)}
			}
		}
		modelID := found.ID
		return m, func() tea.Msg {
			return modelSwapRequestMsg{ModelID: modelID, Provider: found.Provider}
		}
	}
	// F26.6: /resume lists or resumes previous sessions.
	// When main.go wires a real resume handler (wave 4: loads
	// the session into the agent loop, summarizing oversized
	// history), it takes precedence; this builtin is the
	// transcript-only fallback.
	if cmd.Name == "resume" && m.commands["resume"] == nil {
		store := m.sessionStore
		dm := m.marker
		chat := m.chat
		args := cmd.Args
		if store == nil {
			return m, func() tea.Msg {
				return slashResultMsg{Body: dm.Diff("/resume: session store not available")}
			}
		}
		if args == "" {
			return m, func() tea.Msg {
				sessions, err := store.List(10)
				if err != nil {
					return slashResultMsg{Err: fmt.Errorf("resume list: %w", err)}
				}
				if len(sessions) == 0 {
					return slashResultMsg{Body: dm.Diff("No previous sessions found.")}
				}
				var b strings.Builder
				b.WriteString(fmt.Sprintf("_[%d recent session(s)]_\n\n", len(sessions)))
				for _, s := range sessions {
					title := s.Title
					if title == "" {
						title = "(untitled)"
					}
					b.WriteString(fmt.Sprintf("  %s  %s  [%d msgs, %s]\n",
						s.ID[:8], title, s.MessageCount,
						s.UpdatedAt.Format("2006-01-02 15:04")))
				}
				b.WriteString("\nUsage: /resume <session_id>")
				return slashResultMsg{Body: dm.Diff(b.String())}
			}
		}
		// Capture target for closure.
		target := strings.TrimSpace(args)
		return m, func() tea.Msg {
			sess, err := store.Get(target)
			if err != nil {
				return slashResultMsg{Err: fmt.Errorf("resume %s: %w", target, err)}
			}
			msgs, err := store.ReadMessages(context.Background(), target)
			if err != nil {
				return slashResultMsg{Err: fmt.Errorf("resume read %s: %w", target, err)}
			}
			title := sess.Title
			if title == "" {
				title = "(untitled)"
			}
			// Add messages to chat.
			chat.addAssistant(fmt.Sprintf("Resumed session %s: %s (%d messages)",
				sess.ID[:8], title, len(msgs)))
			for _, msg := range msgs {
				if msg.Role == "user" {
					chat.addUser(msg.Content)
				} else if msg.Role == "assistant" {
					chat.addAssistant(msg.Content)
				}
			}
			return slashResultMsg{Body: dm.ModelInfo(fmt.Sprintf("loaded %d messages from session %s", len(msgs), sess.ID[:8]))}
		}
	}
	// F32: /undo — revert last file write/edit operations.
	if cmd.Name == "undo" {
		trk := m.tracker
		dm := m.marker
		args := cmd.Args
		return m, func() tea.Msg {
			if trk == nil {
				return slashResultMsg{Body: dm.Diff("/undo: tracker not wired")}
			}
			n := 1
			if args != "" {
				fmt.Sscanf(args, "%d", &n)
			}
			results, err := trk.Undo(n)
			if err != nil {
				return slashResultMsg{Err: err}
			}
			if len(results) == 0 {
				return slashResultMsg{Body: dm.Diff("/undo: nothing to undo")}
			}
			var b strings.Builder
			fmt.Fprintf(&b, "reverted %d operation(s):\n", len(results))
			for _, r := range results {
				fmt.Fprintf(&b, "  %s (%s)\n", r.Path, r.Op)
			}
			return slashResultMsg{Body: dm.Diff(b.String())}
		}
	}
	if cmd.Name == "providers" {
		mgr := m.providerMgr
		dm := m.marker
		args := cmd.Args
		return m, func() tea.Msg {
			if mgr == nil {
				return slashResultMsg{Body: dm.Diff("/providers: provider manager not wired")}
			}
			mgr.Reload()
			// Parse subcommands.
			parts := strings.Fields(args)
			if len(parts) == 0 {
				// List providers.
				return slashResultMsg{Body: dm.Diff(renderProvidersList(mgr, nil))}
			}
			switch parts[0] {
			case "add":
				if len(parts) < 4 {
					return slashResultMsg{Body: dm.Diff("/providers add <name> <type> <base_url> [api_key]")}
				}
				apiKey := ""
				if len(parts) >= 5 {
					apiKey = parts[4]
				}
				err := mgr.Add(parts[1], parts[2], parts[3], apiKey, "")
				if err != nil {
					return slashResultMsg{Err: err}
				}
				return slashResultMsg{Body: dm.Diff(fmt.Sprintf("provider %q added", parts[1]))}
			case "remove", "rm":
				if len(parts) < 2 {
					return slashResultMsg{Body: dm.Diff("/providers remove <name>")}
				}
				err := mgr.Remove(parts[1])
				if err != nil {
					return slashResultMsg{Err: err}
				}
				return slashResultMsg{Body: dm.Diff(fmt.Sprintf("provider %q removed", parts[1]))}
			case "price":
				if len(parts) < 4 {
					return slashResultMsg{Body: dm.Diff("/providers price <model_id> <input_cost_per_1M> <output_cost_per_1M>")}
				}
				var inputCost, outputCost float64
				fmt.Sscanf(parts[2], "%f", &inputCost)
				fmt.Sscanf(parts[3], "%f", &outputCost)
				err := mgr.SetPrice(parts[1], inputCost, outputCost)
				if err != nil {
					return slashResultMsg{Err: err}
				}
				return slashResultMsg{Body: dm.Diff(fmt.Sprintf("price set for %s: $%.2f/$%.2f per 1M tokens", parts[1], inputCost, outputCost))}
			case "toggle":
				if len(parts) < 2 {
					return slashResultMsg{Body: dm.Diff("/providers toggle <model_id>")}
				}
				hidden := mgr.ToggleHidden(parts[1])
				state := "visible"
				if hidden {
					state = "hidden"
				}
				return slashResultMsg{Body: dm.Diff(fmt.Sprintf("%s is now %s", parts[1], state))}
			default:
				// Treat as provider name — show its details.
				return slashResultMsg{Body: dm.Diff(renderProvidersList(mgr, nil))}
			}
		}
	}
	if cmd.Name == "doctor" {
		// The report now pings local servers and configured
		// providers (network IO) — run it as a background
		// tea.Cmd so the UI never freezes.
		env := doctor.Env{
			Version:     "0.6.0",
			Home:        m.home,
			DataDir:     m.dataDir,
			Provider:    m.llm,
			Registry:    m.toolRegistry,
			Sessions:    m.sessionStore,
			ProviderMgr: m.providerMgr,
			Caps:        m.caps,
		}
		m.appendLine(m.marker.Running())
		m.refreshTranscript()
		return m, func() tea.Msg {
			rep := doctor.Run(context.Background(), env)
			return doctorReportMsg{report: &rep}
		}
	}
	if cmd.Name == "cost" {
		rec := m.statsRecorder
		swapper := m.modelSwapper
		dm := m.marker
		store := m.sessionStore
		return m, func() tea.Msg {
			if rec == nil {
				return slashResultMsg{Body: dm.Diff("/cost: stats not available")}
			}
			turns := rec.Snapshot()
			total := stats.Sum(turns)
			model := ""
			if swapper != nil {
				model = swapper.CurrentModel()
			}
			// Try to get session ID from store.
			sessionID := "current"
			if store != nil {
				if sessions, err := store.List(1); err == nil && len(sessions) > 0 {
					sessionID = sessions[0].ID
				}
			}
			d := cost.Dashboard{
				Turns:     turns,
				Total:     total,
				SessionID: sessionID,
				Model:     model,
			}
			return slashResultMsg{Body: dm.Diff(cost.Render(d))}
		}
	}
	handler, ok := m.commands[cmd.Name]
	if !ok {
		m.appendLine(formatSlashResult("unknown command", fmt.Sprintf("`/%s` is not registered. %s", cmd.Name, RenderHelp())))
		m.refreshTranscript()
		return m, nil
	}
	handler = SafeWrap(cmd.Name, handler)
	m.chat.addUser("> /" + cmd.Name + " " + cmd.Args)
	m.appendLineToTranscript("> /" + cmd.Name + " " + cmd.Args)
	if localSlashCommands[cmd.Name] {
		// Fast local commands (no LLM/network work) must not flip
		// the TUI into the busy/running state: no "running ·
		// Ctrl+C to abort" marker, no working spinner. The handler
		// still runs as a tea.Cmd so a slow disk never freezes the
		// UI; slashResultMsg renders the result.
		m.refreshTranscript()
		return m, func() tea.Msg {
			out, err := handler(context.Background(), cmd.Args)
			return slashResultMsg{Body: out, Err: err}
		}
	}
	m.appendLine(m.marker.Running())
	m.refreshTranscript()
	m.busy = true
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel.Arm(cancelRun, cancel)
	return m, func() tea.Msg {
		defer cancel()
		out, err := handler(ctx, cmd.Args)
		return slashResultMsg{Body: out, Err: err}
	}
}

// localSlashCommands lists commands that do pure in-process work
// (DB reads, formatting) and therefore never show the running
// marker or the busy spinner. Anything that calls the model or
// the network (/council, /compact, /darwin, /login, ...) keeps
// the busy state so Ctrl+C cancellation works.
var localSlashCommands = map[string]bool{
	"help":    true,
	"memory":  true,
	"status":  true,
	"sandbox": true,
	"clear":   true,
	"reflect": true,
	"resume":  true,
	"workers": true,
	"context": true,
}

// shellResultMsg is delivered when a !command finishes.
type shellResultMsg struct {
	res *shellescape.Result
}

// doctorReportMsg delivers an asynchronously computed /doctor report.
type doctorReportMsg struct {
	report *doctor.Report
}

// modelSwapRequestMsg is emitted by /model to request a provider swap.
type modelSwapRequestMsg struct {
	ModelID  string
	Provider string // provider name that owns this model
}

// dispatchShellEscape runs a !command in a goroutine and
// returns a tea.Cmd that emits a shellResultMsg.
func (m Model) dispatchShellEscape(text string) (tea.Model, tea.Cmd) {
	if m.shellRunner == nil {
		m.appendLine(m.marker.Error(fmt.Errorf("shell escape: runner not configured")))
		m.refreshTranscript()
		return m, nil
	}
	cmd := shellescape.ExtractCommand(text)
	m.chat.addUser("> !" + cmd)
	m.appendLineToTranscript("> !" + cmd)
	m.appendLine(m.marker.Running())
	m.refreshTranscript()
	m.busy = true
	return m, func() tea.Msg {
		res := m.shellRunner.Run(context.Background(), cmd)
		return shellResultMsg{res: res}
	}
}

// writeExportFile writes exported content to a file.
func writeExportFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// renderProvidersList renders the provider list with connectivity
// status, model counts, and visibility indicators.
func renderProvidersList(mgr *providers.Manager, caps *llm.CapabilityRegistry) string {
	var b strings.Builder
	infos := mgr.List(caps)
	if len(infos) == 0 {
		return "No providers configured.\n\nAdd one:\n  /providers add <name> <type> <base_url> [api_key]\n\nTypes: openai, opencode, echo"
	}
	for _, pi := range infos {
		status := "✗ disconnected"
		if pi.Connected {
			status = "✓ connected"
		}
		modelCount := len(pi.Models)
		fmt.Fprintf(&b, "%s (%s) — %s | %d model(s)\n",
			pi.Name, pi.Type, status, modelCount)
		if pi.Error != "" {
			fmt.Fprintf(&b, "  error: %s\n", pi.Error)
		}
		if pi.BaseURL != "" {
			fmt.Fprintf(&b, "  base_url: %s\n", pi.BaseURL)
		}
	}
	b.WriteString("\nSubcommands:\n")
	b.WriteString("  /providers add <name> <type> <url> [key]\n")
	b.WriteString("  /providers remove <name>\n")
	b.WriteString("  /providers price <model> <in> <out>\n")
	b.WriteString("  /providers toggle <model>\n")
	return b.String()
}
