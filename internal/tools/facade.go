// Package tools is the public compatibility facade for SuperCli tools.
//
// Implementations live in domain subpackages (core, files, office, web,
// search, ...), but callers can keep importing supercli/internal/tools and use
// the historical API: tools.NewRegistry(), tools.NewReadLines(), tools.Tool,
// tools.Result, etc. This keeps application code stable while the file layout
// stays readable for humans and AI agents.
package tools

import (
	"supercli/internal/tools/core"
	"supercli/internal/tools/files"
	"supercli/internal/tools/interactive"
	"supercli/internal/tools/mail"
	"supercli/internal/tools/media"
	"supercli/internal/tools/memorytools"
	"supercli/internal/tools/office"
	"supercli/internal/tools/search"
	"supercli/internal/tools/skills"
	"supercli/internal/tools/usertools"
	"supercli/internal/tools/web"
	"supercli/internal/tools/workflow"
)

// Core contracts and registry.
type (
	Tool          = core.Tool
	Result        = core.Result
	ImageContent  = core.ImageContent
	Registry      = core.Registry
	CatalogEntry  = core.CatalogEntry
	Check         = core.Check
	VerifyVerdict = core.VerifyVerdict
	Verifier      = core.Verifier
	VerifyFn      = core.VerifyFn
	DefaultVerifier = core.DefaultVerifier
	Category      = core.Category
	Verdict       = core.Verdict
	Action        = core.Action
	Policy        = core.Policy
	Classifier    = core.Classifier
	ErrorRecord   = core.ErrorRecord
	ErrorLog      = core.ErrorLog
)

const (
	CategoryUnknown     = core.CategoryUnknown
	CategoryModel       = core.CategoryModel
	CategoryProgram     = core.CategoryProgram
	CategoryEnvironment = core.CategoryEnvironment

	ActionRetry         = core.ActionRetry
	ActionRetryWithHint = core.ActionRetryWithHint
	ActionSwitchModel   = core.ActionSwitchModel
	ActionLogAndSurface = core.ActionLogAndSurface
	ActionAskUser       = core.ActionAskUser
	ActionSkip          = core.ActionSkip
)

var (
	ErrUnknownTool        = core.ErrUnknownTool
	NewRegistry           = core.NewRegistry
	CoerceArgs            = core.CoerceArgs
	ApplyVerification     = core.ApplyVerification
	NewErrorLog           = core.NewErrorLog
	CatalogEntries        = core.CatalogEntries
	RenderCatalog         = core.RenderCatalog
	EstimateSchemaTokens  = core.EstimateSchemaTokens
	EstimateCatalogTokens = core.EstimateCatalogTokens
)

// File-system tools.
type (
	Copy        = files.Copy
	DeleteLines = files.DeleteLines
	EditLine    = files.EditLine
	EditLines   = files.EditLines
	InsertAfter = files.InsertAfter
	ListDirTool = files.ListDirTool
	MakeDir     = files.MakeDir
	Move        = files.Move
	ReadContext = files.ReadContext
	ReadLines   = files.ReadLines
	ReadMany    = files.ReadMany
	Trash       = files.Trash
	WriteFile   = files.WriteFile
)

var (
	NewCopy        = files.NewCopy
	NewDeleteLines = files.NewDeleteLines
	NewEditLine    = files.NewEditLine
	NewEditLines   = files.NewEditLines
	NewInsertAfter = files.NewInsertAfter
	NewListDir     = files.NewListDir
	NewMakeDir     = files.NewMakeDir
	NewMove        = files.NewMove
	NewReadContext = files.NewReadContext
	NewReadLines   = files.NewReadLines
	NewReadMany    = files.NewReadMany
	NewTrash       = files.NewTrash
	NewWriteFile   = files.NewWriteFile
)

// Office/archive/document tools.
type (
	EditDocxTool = office.EditDocxTool
	EditXlsxTool = office.EditXlsxTool
	ReadDocxTool = office.ReadDocxTool
	ReadPdfTool  = office.ReadPdfTool
	ReadXlsxTool = office.ReadXlsxTool
	ReadZipTool  = office.ReadZipTool
	ZipEntry     = office.ZipEntry
)

const (
	DefaultMaxDocxBytes      = office.DefaultMaxDocxBytes
	DefaultMaxDocxParagraph  = office.DefaultMaxDocxParagraph
	DefaultMaxDocxOutput     = office.DefaultMaxDocxOutput
	DefaultMaxXlsxBytes      = office.DefaultMaxXlsxBytes
	DefaultMaxXlsxCells      = office.DefaultMaxXlsxCells
	DefaultMaxXlsxOutput     = office.DefaultMaxXlsxOutput
	DefaultMaxPdfBytes       = office.DefaultMaxPdfBytes
	DefaultMaxPdfPages       = office.DefaultMaxPdfPages
	DefaultMaxPdfOutput      = office.DefaultMaxPdfOutput
	DefaultMaxZipBytes       = office.DefaultMaxZipBytes
	DefaultMaxZipEntries     = office.DefaultMaxZipEntries
	DefaultMaxExtractedBytes = office.DefaultMaxExtractedBytes
	DefaultMaxSingleFileBytes = office.DefaultMaxSingleFileBytes
)

var (
	NewEditDocx = office.NewEditDocx
	NewEditXlsx = office.NewEditXlsx
	NewReadDocx = office.NewReadDocx
	NewReadPdf  = office.NewReadPdf
	NewReadXlsx = office.NewReadXlsx
	NewReadZip  = office.NewReadZip
	IsXlsx      = office.IsXlsx
)

// Web tools.
type (
	WebFetch        = web.WebFetch
	WebSearch       = web.WebSearch
	WebSearchResult = web.WebSearchResult
)

var (
	NewWebFetch  = web.NewWebFetch
	NewWebSearch = web.NewWebSearch
)

// Search/meta tools.
type (
	Index        = search.Index
	IndexedTool  = search.IndexedTool
	SearchResult = search.SearchResult
	SearchCode   = search.SearchCode
	SearchHistory = search.SearchHistory
	ToolSearcher = search.ToolSearcher
)

var (
	NewInMemoryIndex = search.NewInMemoryIndex
	NewFileIndex     = search.NewFileIndex
	NewSearchCode    = search.NewSearchCode
	NewSearchHistory = search.NewSearchHistory
	NewToolSearcher  = search.NewToolSearcher
)

// Memory tools.
type (
	MemoryKeeper   = memorytools.MemoryKeeper
	HybridSearcher = memorytools.HybridSearcher
	Remember       = memorytools.Remember
	Recall         = memorytools.Recall
)

var (
	NewRemember     = memorytools.NewRemember
	NewRememberDual = memorytools.NewRememberDual
	NewRecall       = memorytools.NewRecall
	NewRecallDual   = memorytools.NewRecallDual
)

// User tools.
type (
	UserToolDef    = usertools.UserToolDef
	UserToolLoader = usertools.UserToolLoader
)

var (
	ErrUnsafeCommand = usertools.ErrUnsafeCommand
	NewUserToolLoader = usertools.NewUserToolLoader
)

// Skills.
type (
	Skill        = skills.Skill
	Source       = skills.Source
	Discoverer   = skills.Discoverer
	SkillApplier = skills.SkillApplier
)

var (
	NewDiscoverer   = skills.NewDiscoverer
	NewSkillApplier = skills.NewSkillApplier
)

// Media / user interaction.
type (
	ReadImageTool      = media.ReadImageTool
	SendScreenshotTool = media.SendScreenshotTool
	ClipboardCapture   = media.ClipboardCapture
	AskUser            = interactive.AskUser
	AskRequest         = interactive.AskRequest
	AskOption          = interactive.AskOption
	AskAnswer          = interactive.AskAnswer
)

const DefaultMaxScreenshotBytes = media.DefaultMaxScreenshotBytes

var (
	NewReadImage      = media.NewReadImage
	SupportedImageMIMEs = media.SupportedImageMIMEs
	NewSendScreenshot = media.NewSendScreenshot
	NewAskUser        = interactive.NewAskUser
)

// Workflow / agent-facing tools.
type (
	CheckLibraryAlternatives = workflow.CheckLibraryAlternatives
	Consult                  = workflow.Consult
	CtxExecuteTool           = workflow.CtxExecuteTool
	GoalTool                 = workflow.GoalTool
	HideMessages             = workflow.HideMessages
	Hider                    = workflow.Hider
)

var (
	NewCheckLibraryAlternatives = workflow.NewCheckLibraryAlternatives
	NewConsult                  = workflow.NewConsult
	NewCtxExecuteTool           = workflow.NewCtxExecuteTool
	NewGoalTool                 = workflow.NewGoalTool
	NewHideMessages             = workflow.NewHideMessages
)

// Mail.
type OutlookMail = mail.OutlookMail

var NewOutlookMail = mail.NewOutlookMail
