// Package tui is the Bubble Tea presentation layer. F25 replaces
// the raw transcript with a structured chat view (role-based
// colors), adds a status bar, inline event markers, a tool-
// name spinner, Ctrl+C run cancellation, and PgUp/PgDn scrolling.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"supercli/internal/ui/viewport"

	"supercli/internal/agent"
	"supercli/internal/llm"
	"supercli/internal/llm/providers"
	"supercli/internal/storage/goal"
	"supercli/internal/storage/session"
	"supercli/internal/system/config"
	"supercli/internal/system/doctor"
	"supercli/internal/system/stats"
	"supercli/internal/tools"
	"supercli/internal/tools/fileops"
	"supercli/internal/tools/shellescape"
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
	home                 string
	dataDir              string
	sessionID            string
	loadedSessionID      string
	pendingAttachments   []string
	attachmentPickerOpen bool

	// version feeds the slim header bar
	// ("✻ SuperCli 0.6.0 · model · tier").
	version  string
	language string
	agent    agent.Agent
	llm      llm.Provider

	// commands is a map of slash command name -> handler.
	// Set via New and used by handleKey to dispatch
	// `/`-prefixed input. nil = no slash commands.
	commands map[string]SlashHandler

	// statusFn returns the footer status line. nil
	// disables the status line entirely. F7.
	statusFn    func() string
	dashboardFn func() DashboardSnapshot

	// F25: theme and styled markers
	palette     Palette
	renderCache *tuiRenderCache
	marker      Marker

	// Widgets
	viewport           viewport.Model
	viewportContent    string
	viewportContentSet bool
	input              textarea.Model
	spinner            spinner.Model

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
	pendingAsk  *pendingAsk
	askQueue    []tools.AskRequest
	toolNames   map[string]string
	workerViews []workerView

	// F26.2: shellRunner handles "!command" shell escapes.
	// nil = shell escapes disabled.
	shellRunner *shellescape.Runner

	// F26.3: planMode toggles read-only analysis mode.
	planMode bool

	// F26.4: tracker records file changes for /diff.
	tracker *fileops.Tracker

	// F26.5: modelSwapper for /model hot-swap.
	modelSwapper         ModelSwapper
	modelLister          ModelLister
	modelSwapFn          ModelSwapFunc
	codexAccountsChanged bool
	resumeSession        func(context.Context, string, []llm.Message, []string) error
	resumeContext        context.Context
	prepareResume        func(context.Context, string) error
	initialResumeCmd     tea.Cmd
	drafts               *DraftRecovery
	submittingDraft      string
	cancelling           bool
	draftErrorShown      string
	sessionStore         *session.Store
	statsRecorder        stats.Recorder     // F28: per-turn metrics for /cost
	providerMgr          *providers.Manager // F30: provider management
	activeProvider       string
	modelContexts        *config.ModelContextStore
	caps                 *llm.CapabilityRegistry
	goalSvc              *goal.Service
	globalGoalSvc        *goal.Service
	goalUnassigned       []*goal.Goal
	goalMenuTasks        []goal.Task
	goalMenuHistory      []*goal.Goal
	toolRegistry         *tools.Registry
	doctorReport         *doctor.Report
	menu                 interactiveMenu
	previewURL           string
	previewID            uint64
	autocomp             autocomplete // autocomplete popup state
	// providerStatuses caches async connectivity probe results for
	// the /providers menu (key: provider name). The menu renders
	// instantly with "checking..." and statuses pop in as the
	// background pings finish.
	providerStatuses    map[string]providerStatus
	modelPickerScanning bool

	// onRunEnd is invoked (in a goroutine) after each agent run
	// finishes. See Options.OnRunEnd.
	onRunEnd func()

	// onRunStart is invoked synchronously when the user submits
	// a prompt, before the run begins. See Options.OnRunStart.
	onRunStart        func()
	checkpointUndo    func(context.Context, bool) (string, error)
	checkpointPreview func(redo bool) (CheckpointPreview, error)
	dataExport        func(context.Context, bool) (string, error)
	dataImport        func(context.Context, string) (bool, error)

	// statusOverride holds a temporary status message (e.g.
	// "cancelled") that replaces the normal status bar for
	// a few seconds. Cleared by statusOverrideClearMsg.
	statusOverride string
	statusSuccess  bool
	statusRevision uint64

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
	// Pointer ownership keeps Builder safe when Bubble Tea copies Model values.
	currentBuffer *strings.Builder
	// The last painted inputs let timer ticks skip unchanged Markdown/layout.
	renderedCurrent string
	renderedSpinner string
	streamPaintAt   time.Time

	// responseLen tracks the total character count of the
	// current assistant response. Used for chars/4 token
	// estimation when the provider doesn't report usage.
	responseLen int

	// lastToolName stores the name of the most recent tool
	// call. Used to label the tool result in the chat.
	lastToolName string
	toolActivity toolActivity

	// runtimeHUD is refreshed once after a completed turn. It is deliberately
	// not recomputed from the full conversation in View(), keeping redraws and
	// token streaming free of context-estimation work.
	runtimeHUD         string
	runtimeContext     contextSnapshot
	reasoningOpen      bool
	nativeReasoningEnd int // end of the host-created leading thought, before its close tag

	// toolExpanded: when true, tool results are shown in
	// full (max 50 lines). Toggled with 'E' key.
	toolExpanded bool

	// tipShown guards the "use Ctrl+C or /quit to exit" hint so
	// it is printed at most once per session (it used to be
	// appended on every empty-input Esc/Enter, spamming the
	// transcript).
	tipShown bool

	// hasTranscript preserves the legacy empty/welcome decision without a
	// second copy of the completed text already owned by chat.
	hasTranscript bool
}

// SlashHandler is the signature for a TUI slash command
// (e.g. `/darwin 3 fix bug`). The handler runs in a
// goroutine; it must respect ctx for cancellation.
type SlashHandler func(ctx context.Context, args string) (string, error)

// pendingAsk is the live state of an active ask_user interaction.
type pendingAsk struct {
	Confirmation bool
	detailOffset int
	ID           string
	done         <-chan struct{}
	Question     string
	Header       string
	Options      []tools.AskOption
	MultiSelect  bool
	AllowCustom  bool
	customMode   bool
	custom       string
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
	Home    string
	DataDir string
	// Language is the shared UI locale, detected and persisted by
	// the executable before constructing the TUI.
	Language string
	// ShowGenerationSpeed controls completed-turn delivery throughput. Nil is on.
	ShowGenerationSpeed *bool
	// SessionID identifies the live conversation so the interactive
	// session picker can omit it from the "continue session" list.
	SessionID     string
	ResumeID      string
	DraftRecovery *DraftRecovery
	PrepareResume func(context.Context, string) error
	ResumeSession func(context.Context, string, []llm.Message, []string) error
	Agent         agent.Agent
	LLM           llm.Provider
	Commands      map[string]SlashHandler
	// StatusFn, if non-nil, is called from View() to
	// render the footer status line (typically
	// "credits: 1.2k/10k (12%)"). The TUI does not own
	// the credit state; main.go does.
	StatusFn func() string
	// DashboardFn supplies cheap, structured footer data.
	DashboardFn func() DashboardSnapshot
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
	// ActiveProvider is the configured connection/profile name owning the
	// initial model. ModelContextStore persists provider+model context budgets.
	ActiveProvider    string
	ModelContextStore *config.ModelContextStore
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
	// OnRunStart, if non-nil, is called synchronously the moment
	// the user submits a prompt for the agent, BEFORE the run
	// begins. main.go uses it to cancel background memory
	// inference so the foreground turn never queues behind it.
	// Must be fast and non-blocking.
	OnRunStart func()
	// CheckpointUndo performs a conflict-safe whole-turn undo/redo. The bool
	// is true for redo. When set it supersedes the legacy operation tracker.
	CheckpointUndo func(context.Context, bool) (string, error)
	// CheckpointPreview reports the files affected by undo/redo without changing
	// them. When present, the action centre asks for confirmation first.
	CheckpointPreview func(redo bool) (CheckpointPreview, error)
	// DataExport and DataImport back the local backup panel. They run in a
	// Bubble Tea command goroutine, so large archives never block rendering.
	DataExport func(context.Context, bool) (string, error)
	DataImport func(context.Context, string) (bool, error)
	// Version is shown in the header bar (e.g. "0.6.0").
	Version string
	// Tier is the active model tier shown in the header
	// (e.g. "big", "small"). Optional.
	Tier string
}

type toolActivity struct {
	calls         int
	errors        int
	repeats       int
	lastSignature string
	byName        map[string]int
}

func (a *toolActivity) reset() {
	*a = toolActivity{byName: make(map[string]int)}
}

func (a *toolActivity) call(name, args string) {
	if a.byName == nil {
		a.byName = make(map[string]int)
	}
	a.calls++
	a.byName[name]++
	sig := name + "\x00" + strings.TrimSpace(args)
	if sig == a.lastSignature {
		a.repeats++
	}
	a.lastSignature = sig
}

// ModelSwapper is implemented by agent.Loop for /model hot-swap.
type ModelSwapper interface {
	CurrentModel() string
	SetModel(p llm.Provider)
}

type contextProviderSetter interface {
	SetContextProvider(provider string)
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
	opts.Language = normalizeLanguage(opts.Language)
	ti := newInputArea()
	ti.Placeholder = textFor(opts.Language, "tui.menu_settings_actions.2d53d0e1de")
	ti.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Points

	vp := viewport.New(80, 20)

	// F25: build the palette. NoColor → plain text.
	p := DefaultPalette()
	if opts.NoColor {
		p = NoColorPalette()
	}
	mkr := NewMarker(p, opts.Language)
	sp.Style = p.Header // accent-colored spinner animation
	ti.FocusedStyle.Prompt = p.InputPrompt
	ti.FocusedStyle.Text = p.InputText
	ti.FocusedStyle.Placeholder = p.InputHint
	ti.FocusedStyle.CursorLine = p.InputText
	ti.BlurredStyle.Prompt = p.InputPrompt
	ti.BlurredStyle.Text = p.InputText
	ti.BlurredStyle.Placeholder = p.InputHint

	vp.SetContent(welcome(opts, p))

	m := Model{
		home:              opts.Home,
		dataDir:           opts.DataDir,
		sessionID:         opts.SessionID,
		version:           opts.Version,
		language:          opts.Language,
		agent:             opts.Agent,
		llm:               opts.LLM,
		commands:          opts.Commands,
		statusFn:          opts.StatusFn,
		dashboardFn:       opts.DashboardFn,
		palette:           p,
		renderCache:       new(tuiRenderCache),
		marker:            mkr,
		extCh:             opts.ExtCh,
		viewport:          vp,
		input:             ti,
		spinner:           sp,
		chat:              newChat(80, opts.Language),
		cancel:            NewCancelState(),
		scroll:            ScrollConfig{},
		shellRunner:       opts.ShellRunner,
		tracker:           opts.Tracker,
		modelSwapper:      opts.ModelSwapper,
		modelLister:       opts.ModelLister,
		modelSwapFn:       opts.ModelSwapFn,
		resumeSession:     opts.ResumeSession,
		prepareResume:     opts.PrepareResume,
		drafts:            opts.DraftRecovery,
		sessionStore:      opts.SessionStore,
		statsRecorder:     opts.StatsRecorder,
		providerMgr:       opts.ProviderMgr,
		activeProvider:    opts.ActiveProvider,
		modelContexts:     opts.ModelContextStore,
		caps:              opts.CapabilityRegistry,
		goalSvc:           opts.GoalService,
		toolRegistry:      opts.ToolRegistry,
		onRunEnd:          opts.OnRunEnd,
		onRunStart:        opts.OnRunStart,
		checkpointUndo:    opts.CheckpointUndo,
		checkpointPreview: opts.CheckpointPreview,
		dataExport:        opts.DataExport,
		dataImport:        opts.DataImport,
	}
	m.chat.hideGenerationSpeed = opts.ShowGenerationSpeed != nil && !*opts.ShowGenerationSpeed
	if m.drafts != nil {
		saved := m.drafts.Snapshot()
		m.input.SetValue(saved.Text)
		m.pendingAttachments = saved.Attachments
		m.syncInputHeight()
		if saved.Text != "" || len(saved.Attachments) > 0 {
			m.setStatus(m.tr("tui.model.6f2da678de"), true)
			if opts.ResumeID == "" && m.sessionStore != nil && saved.SessionID != "" {
				if sess, err := m.sessionStore.Get(saved.SessionID); err == nil && sess.MessageCount > 0 {
					opts.ResumeID = saved.SessionID
				}
			}
		}
	}
	if opts.ResumeID != "" {
		next, cmd := m.resumeConversation(opts.ResumeID)
		m = next.(Model)
		m.initialResumeCmd = cmd
	}
	return m
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
	ti.Placeholder = textFor("en", "tui.menu_settings_actions.2d53d0e1de")
	// Keep the primary prompt ASCII-safe. A surprising number of Windows
	// terminal/font combinations render the former ❯ glyph as an empty box.
	ti.Prompt = "> "
	ti.CharLimit = 0
	ti.ShowLineNumbers = false
	ti.MaxHeight = maxInputLines
	ti.SetHeight(1)
	// Enter is intercepted by handleKey to send the message;
	// rebind newline insertion to Alt+Enter / Ctrl+J.
	ti.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "ctrl+j"),
		key.WithHelp("alt+enter", textFor("en", "tui.other.c7cf1c599b")),
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
	m.resizeViewport()
}

// Init satisfies tea.Model. The spinner ticks while the agent
// is running; the text input cursor blinks.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, textarea.Blink, m.initialResumeCmd}
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
	mentionCount      int
	mentionTokens     int
	draft             string
	attachmentsSent   bool
	attachmentWarning string
	queueWarning      error
}

type runEventMsg struct {
	ev agent.Event
}

type runEndMsg struct{}

type dataOperationMsg struct {
	kind string
	path string
	full bool
	err  error
}

// statusOverrideClearMsg clears the temporary status override
// (e.g. "cancelled" → normal status).
type statusOverrideClearMsg struct{ revision uint64 }

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
	Body            string
	Err             error
	History         *resumedTranscript
	Document        bool
	Local           bool
	RefreshAccounts bool
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
func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.resizeViewport()
		m.chat.width = msg.Width
		m.chat.completedDirty = true
		m.input.SetWidth(msg.Width)
		// Keep the empty-state welcome responsive. Once a conversation has
		// started refreshTranscript owns the viewport and resize must never
		// replace the chat with the welcome screen.
		if !m.hasTranscript && m.current == "" {
			m.setViewportContent(welcomeAtSize(Options{LLM: m.llm, Language: m.language}, m.palette, msg.Width, msg.Height))
		} else {
			m.refreshTranscript()
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.updateKeyMessage(msg)

	case previewPasteMsg:
		return m.applyPreviewPaste(msg)

	case previewOpenedMsg:
		return m.applyPreviewOpened(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case runStartMsg:
		m.submittingDraft = ""
		if msg.err != nil {
			m.cancelling = false
			// agent.Run failed before the run started.
			m.cancel.Disarm()
			m.busy = false
			if m.input.Value() == "" && msg.draft != "" {
				m.input.SetValue(msg.draft)
				m.syncInputHeight()
			}
			m.appendLine(m.marker.Error(msg.err))
			m.refreshTranscript()
			return m, nil
		}
		if msg.queueWarning != nil {
			m.appendLine(m.marker.Error(fmt.Errorf("%s%w", m.tr("tui.menu_workflow.6d90ed4e2a"), msg.queueWarning)))
			m.refreshTranscript()
		}
		if msg.attachmentsSent {
			m.pendingAttachments = nil
			m.syncInputHeight()
		}
		if msg.attachmentWarning != "" {
			m.appendLine(m.marker.Error(fmt.Errorf(m.tr("tui.model.9d4ca46626"), msg.attachmentWarning)))
		}
		if msg.mentionCount > 0 && msg.mentionTokens > 0 {
			m.appendLine(m.marker.Mention(msg.mentionCount, msg.mentionTokens))
			m.refreshTranscript()
		}
		m.busy = true
		m.resetCurrent()
		m.responseLen = 0
		m.toolActivity.reset()
		m.toolNames = make(map[string]string)
		m.eventCh = msg.ch
		return m, tea.Batch(waitForEvent(msg.ch), streamFlushCmd())

	case runEventMsg:
		return m.handleAgentEvent(msg.ev)

	case runExtEventMsg:
		newM, _ := m.handleAgentEvent(msg.ev)
		return newM, waitForExternalEvent(m.extCh)

	case runEndMsg:
		m.cancelling = false
		m.busy = false
		m.cancel.Disarm()
		m.eventCh = nil
		if m.onRunEnd != nil {
			go m.onRunEnd()
		}
		m.refreshTranscript()
		// Preserve a draft typed while the final provider delta was landing.
		// Submitted interjections reset themselves; an unsent draft must not
		// disappear merely because the run ended first.
		m.syncInputHeight()
		m.input.Focus()
		return m, nil

	case dataOperationMsg:
		if msg.err != nil {
			m.setStatus(m.tr("tui.model.68fdbe4d9b")+msg.err.Error(), false)
			return m, nil
		}
		m.mode = modeNormal
		m.menu = interactiveMenu{}
		m.input.Focus()
		if msg.kind == "import" {
			m.setStatus(m.tr("tui.model.62b14214ce"), true)
			m.appendLine(m.palette.Success.Render(m.tr("tui.model.f5e7fbcac3")) + m.statusOverride)
		} else {
			m.setStatus(m.tr("tui.model.51ae95b11d")+msg.path, true)
			m.appendLine(m.palette.Success.Render(m.tr("tui.model.f5e7fbcac3")) + m.statusOverride)
		}
		m.refreshTranscript()
		return m, nil

	case askRequestMsg:
		return m.beginAsk(msg.req)
	case askClosedMsg:
		if m.pendingAsk != nil && m.pendingAsk.ID == string(msg) {
			m.endAsk()
		}
		return m, nil

	case resumeLoadedMsg:
		return m.finishResume(msg)
	case clipboardImageMsg:
		return m.finishClipboardImage(msg)
	case nativeAttachmentsMsg:
		return m.applyNativeAttachments(msg)

	case attachmentDirectoryMsg:
		if m.menu.kind == menuAttachments && m.menu.attachmentDir == msg.path {
			m.menu.attachmentLoading = false
			m.menu.attachmentEntries = msg.entries
			if msg.err != nil {
				m.menu.formErr = msg.err.Error()
			}
		}
		return m, nil
	case usageLoadedMsg:
		if m.menu.kind == menuUsage && m.menu.category == msg.scope && (msg.request == nil || msg.request == m.menu.usage) {
			if msg.err != nil {
				m.menu.formErr = msg.err.Error()
			} else {
				m.menu.usage = msg.data
				m.menu.formErr = ""
			}
		}
		return m, nil
	case providerStatusMsg:
		if m.providerStatuses == nil {
			m.providerStatuses = make(map[string]providerStatus)
		}
		m.providerStatuses[msg.name] = providerStatus{checked: true, online: msg.online, err: msg.err, latency: msg.latency, checkedAt: msg.checkedAt}
		return m, nil

	case providerProtocolDetectedMsg:
		return m.finishProviderDetection(msg)

	case providerSavedMsg:
		if msg.err != nil {
			formAt := 0
			if len(msg.form) > 3 {
				formAt = 3 // credentials are the most likely verification failure
			} else if len(msg.form) > 0 {
				formAt = len(msg.form) - 1
			}
			formErr := m.tr("tui.model.546dd6f834")
			formEditName := msg.editName
			switch {
			case msg.wasNew && msg.rolledBack:
				formErr += m.tr("tui.model.6cb2c32028")
				formEditName = ""
			case msg.wasNew && msg.rollbackErr != nil:
				formErr += m.tr("tui.model.31ff0d62e8")
				formErr += m.tr("tui.model.6899286b47") + compactProviderError(msg.rollbackErr) + ". "
				formEditName = msg.name
			default:
				formErr += m.tr("tui.model.b08e428273")
			}
			formErr += compactProviderError(msg.err)
			m.enterMenu(interactiveMenu{
				kind:     menuProviderForm,
				form:     append([]string(nil), msg.form...),
				formAt:   formAt,
				formErr:  formErr,
				editName: formEditName,
			})
			if m.providerStatuses != nil && msg.rolledBack {
				delete(m.providerStatuses, msg.name)
			}
			return m, nil
		} else {
			m.appendLine(m.palette.InputHint.Render(m.tr("tui.menu_keys.7170173a8d") + msg.name + ": " + msg.body))
		}
		m.refreshTranscript()
		return m, nil

	case modelPickerScanDoneMsg:
		m.modelPickerScanning = false
		if m.mode == modeMenu {
			m.clampMenuCursor()
		}
		return m, nil

	case providerScanDoneMsg:
		if m.mode == modeMenu {
			m.clampMenuCursor()
		}
		return m, nil

	case doctorReportMsg:
		m.chat.removeLastSystem(m.marker.Running())
		m.mode = modeDoctor
		m.doctorReport = msg.report
		return m, nil

	case slashResultMsg:
		if msg.Err == nil && msg.RefreshAccounts {
			m.codexAccountsChanged = true
		}
		if !msg.Local {
			m.busy = false
			m.cancel.Disarm()
			m.chat.removeLastSystem(m.marker.Running())
		}
		if msg.Err != nil {
			m.appendLine(m.marker.Error(msg.Err))
		} else if msg.History != nil {
			m.applyResumedTranscript(msg.History)
		} else if msg.Document {
			m.chat.addDocument(msg.Body)
			m.appendLineToTranscript(msg.Body)
		} else {
			m.appendLine(msg.Body)
		}
		m.refreshTranscript()
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
				fmt.Sprintf(m.tr("tui.model.9482205df6"),
					r.Command, r.ExitCode, r.Duration.Round(1e6), out), true))
		} else {
			out := strings.TrimSpace(r.Stdout)
			if out == "" {
				out = m.tr("tui.view_markers.efefc15c24")
			}
			m.appendLine(m.marker.ToolResult(
				fmt.Sprintf("!%s (%v)\n%s",
					r.Command, r.Duration.Round(1e6), out), false))
		}
		m.refreshTranscript()
		m.syncInputHeight()
		m.input.Focus()
		return m, nil

	case statusOverrideClearMsg:
		if msg.revision != 0 && msg.revision != m.statusRevision {
			return m, nil
		}
		m.setStatus("", false)
		return m, nil

	case statusRefreshMsg:
		m.resizeViewport()
		// Background data (e.g. an async Codex usage snapshot)
		// arrived. Returning triggers a View() re-render so the
		// pull-based footer picks it up without a keystroke.
		return m, nil

	case streamFlushMsg:
		// Force a View() render during active streaming.
		// The tick stops when eventCh becomes nil (run ends).
		if m.eventCh != nil {
			if m.chat.completedDirty || m.current != m.renderedCurrent || m.streamSpinner() != m.renderedSpinner {
				m.refreshTranscript()
			}
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
		m.appendLine(m.marker.ModelInfo(fmt.Sprintf(m.tr("tui.model.cf4041c413"), modelID)))
		return
	}
	// modelSwapFn rebuilds the provider AND (for Codex providers)
	// fires kickCodexUsageRefresh in the background, so the HUD
	// limit tile refreshes on its own right after the swap — no
	// message send required.
	newProv, err := m.modelSwapFn(modelID, provider)
	if err != nil {
		m.appendLine(m.marker.Error(fmt.Errorf(m.tr("tui.model.7bc54e9db4"), err)))
		return
	}
	m.modelSwapper.SetModel(newProv)
	if setter, ok := m.modelSwapper.(contextProviderSetter); ok {
		setter.SetContextProvider(provider)
	}
	m.activeProvider = provider
	m.llm = newProv // update header display
	m.refreshRuntimeHUD()
	// Show the model the user picked, not the provider's internal
	// Name() (a multi-account router reports "router(N providers)",
	// which would leak here). modelID is what the user chose.
	swapLabel := modelID
	if swapLabel == "" {
		swapLabel = newProv.Name()
	}
	m.appendLine(m.marker.ModelInfo(fmt.Sprintf(m.tr("tui.model.2f3dd8cd86"), swapLabel)))
	// Persist active model + provider for next startup. Done here
	// (at confirm time) rather than lazily at the next send, so
	// closing the CLI immediately after picking keeps the choice.
	if m.providerMgr != nil {
		_ = m.providerMgr.SaveActiveConfig(modelID, provider)
	}
}

type interjectionQueuer interface {
	QueueInterjection(string) bool
}

// handleBusyInput keeps the composer usable while the model or a tool is
// working. Enter queues a normal user message for the next safe loop boundary;
// all other text editing stays entirely local to Bubble Tea.
