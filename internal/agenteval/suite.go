// Package agenteval provides an offline-testable, fixture-driven quality
// harness for SuperCli. It is intentionally outside the runtime agent loop:
// normal sessions pay no startup, prompt, or allocation cost for evals.
package agenteval

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Suite describes a stable collection of agent tasks.
type Suite struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// IgnoreChangedFiles are glob patterns (see MatchGlob) for files the
	// diff scoring never counts, e.g. interpreter caches.
	IgnoreChangedFiles []string `json:"ignoreChangedFiles,omitempty"`
	Tasks              []Task   `json:"tasks"`
}

// Task is evaluated in a fresh copy of WorkspaceFixture.
type Task struct {
	ID                    string                `json:"id"`
	Name                  string                `json:"name"`
	Description           string                `json:"description,omitempty"`
	Tags                  []string              `json:"tags,omitempty"`
	Difficulty            string                `json:"difficulty,omitempty"`
	Prompt                string                `json:"prompt"`
	WorkspaceFixture      string                `json:"workspaceFixture"`
	ExpectedChangedFiles  []string              `json:"expectedChangedFiles,omitempty"`
	ForbiddenChangedFiles []string              `json:"forbiddenChangedFiles,omitempty"`
	RequiredTraceEvents   []string              `json:"requiredTraceEvents,omitempty"`
	VerificationCommands  []VerificationCommand `json:"verificationCommands,omitempty"`

	// Category groups tasks in reports (coding, understanding, ...).
	Category string `json:"category,omitempty"`
	// Network marks tasks that need internet access.
	Network bool `json:"network,omitempty"`
	// TimeoutSeconds overrides the bench-wide per-run timeout.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
	// AgentConfig is a TOML fragment a runner may write into the
	// isolated agent config (e.g. a smaller context_window).
	AgentConfig string `json:"agentConfig,omitempty"`
	// GraderOverlay is a fixture-relative directory copied over the
	// workspace after the agent finished and the diff was taken, so
	// hidden tests stay invisible to the agent.
	GraderOverlay string `json:"graderOverlay,omitempty"`
	// StrictDiff makes every changed file that does not match
	// AllowedChangedFiles (globs) a disallowed change.
	StrictDiff          bool          `json:"strictDiff,omitempty"`
	AllowedChangedFiles []string      `json:"allowedChangedFiles,omitempty"`
	FileChecks          []FileCheck   `json:"fileChecks,omitempty"`
	AnswerChecks        []AnswerCheck `json:"answerChecks,omitempty"`
}

// FileCheck asserts the content of one workspace file (Path may be a
// glob; every match is checked and at least one must exist unless
// Exists is false).
type FileCheck struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	Exists      *bool    `json:"exists,omitempty"`
	Contains    []string `json:"contains,omitempty"`
	NotContains []string `json:"notContains,omitempty"`
	Regex       []string `json:"regex,omitempty"`
	NotRegex    []string `json:"notRegex,omitempty"`
	// Equals compares after CRLF->LF and trailing-whitespace trimming.
	Equals *string `json:"equals,omitempty"`
	// MaxChangedLines bounds added+removed lines against the fixture.
	MaxChangedLines *int `json:"maxChangedLines,omitempty"`
	// MaxOccurrences bounds how often a literal may appear.
	MaxOccurrences map[string]int `json:"maxOccurrences,omitempty"`
	Weight         float64        `json:"weight,omitempty"`
	Critical       bool           `json:"critical,omitempty"`
}

// AnswerCheck grades the agent's final answer text. Patterns are
// case-insensitive substrings, or case-insensitive regexps when Regex
// is set. Backslashes in the answer are normalized to slashes.
type AnswerCheck struct {
	ID       string   `json:"id"`
	AnyOf    []string `json:"anyOf,omitempty"`
	AllOf    []string `json:"allOf,omitempty"`
	NoneOf   []string `json:"noneOf,omitempty"`
	Regex    bool     `json:"regex,omitempty"`
	Weight   float64  `json:"weight,omitempty"`
	Critical bool     `json:"critical,omitempty"`
}

// VerificationCommand uses argv rather than a shell string. This keeps the
// harness deterministic and prevents prompts or fixture paths from becoming
// shell interpolation input.
type VerificationCommand struct {
	ID   string   `json:"id"`
	Args []string `json:"args"`
	// Env adds KEY=VALUE entries to the inherited environment.
	Env []string `json:"env,omitempty"`
	// ExpectOutput, when set, must equal the trimmed combined output.
	ExpectOutput *string `json:"expectOutput,omitempty"`
	Weight       float64 `json:"weight,omitempty"`
	// Optional commands only lower the score; by default a failing
	// verification command fails the run.
	Optional bool `json:"optional,omitempty"`
}

// LoadSuite decodes a suite strictly and validates its portable contract.
func LoadSuite(path string) (Suite, error) {
	f, err := os.Open(path)
	if err != nil {
		return Suite{}, fmt.Errorf("open eval suite: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var suite Suite
	if err := dec.Decode(&suite); err != nil {
		return Suite{}, fmt.Errorf("decode eval suite: %w", err)
	}
	if err := ensureEOF(dec); err != nil {
		return Suite{}, err
	}
	if err := suite.Validate(filepath.Dir(path)); err != nil {
		return Suite{}, err
	}
	return suite, nil
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode eval suite trailer: %w", err)
	}
	return fmt.Errorf("eval suite contains more than one JSON value")
}

// Validate rejects ambiguous IDs, unsafe fixture paths, and tasks that cannot
// produce an objective pass/fail result.
func (s Suite) Validate(suiteDir string) error {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("eval suite id and name are required")
	}
	if len(s.Tasks) == 0 {
		return fmt.Errorf("eval suite %q has no tasks", s.ID)
	}
	seen := make(map[string]bool, len(s.Tasks))
	for i, task := range s.Tasks {
		if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.Name) == "" || strings.TrimSpace(task.Prompt) == "" {
			return fmt.Errorf("eval task %d requires id, name, and prompt", i+1)
		}
		if seen[task.ID] {
			return fmt.Errorf("duplicate eval task id %q", task.ID)
		}
		seen[task.ID] = true
		if !safeRelative(task.WorkspaceFixture) {
			return fmt.Errorf("eval task %q has unsafe workspaceFixture %q", task.ID, task.WorkspaceFixture)
		}
		fixture := filepath.Join(suiteDir, filepath.FromSlash(task.WorkspaceFixture))
		if info, err := os.Stat(fixture); err != nil || !info.IsDir() {
			return fmt.Errorf("eval task %q fixture is unavailable: %s", task.ID, fixture)
		}
		for _, path := range append(append([]string(nil), task.ExpectedChangedFiles...), task.ForbiddenChangedFiles...) {
			if !safeRelative(path) {
				return fmt.Errorf("eval task %q has unsafe file expectation %q", task.ID, path)
			}
		}
		if len(task.VerificationCommands) == 0 && len(task.FileChecks) == 0 && len(task.AnswerChecks) == 0 {
			return fmt.Errorf("eval task %q has no verificationCommands, fileChecks or answerChecks", task.ID)
		}
		if task.GraderOverlay != "" {
			overlay := filepath.Join(suiteDir, filepath.FromSlash(task.GraderOverlay))
			if !safeRelative(task.GraderOverlay) {
				return fmt.Errorf("eval task %q has unsafe graderOverlay %q", task.ID, task.GraderOverlay)
			}
			if info, err := os.Stat(overlay); err != nil || !info.IsDir() {
				return fmt.Errorf("eval task %q grader overlay is unavailable: %s", task.ID, overlay)
			}
		}
		if err := validateGraders(task); err != nil {
			return fmt.Errorf("eval task %q: %w", task.ID, err)
		}
		commandIDs := map[string]bool{}
		for _, command := range task.VerificationCommands {
			if strings.TrimSpace(command.ID) == "" || len(command.Args) == 0 || strings.TrimSpace(command.Args[0]) == "" {
				return fmt.Errorf("eval task %q has an invalid verification command", task.ID)
			}
			if commandIDs[command.ID] {
				return fmt.Errorf("eval task %q repeats verification command id %q", task.ID, command.ID)
			}
			commandIDs[command.ID] = true
		}
	}
	return nil
}

func safeRelative(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}
