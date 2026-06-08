// Package export renders session transcripts as Markdown files.
// Used by /export to let users save conversations outside SQLite.
package export

import (
	"fmt"
	"strings"
	"time"

	"supercli/internal/session"
)

// Options controls the export output.
type Options struct {
	// Session metadata (pulled from session.Session).
	ID        string
	Title     string
	Model     string
	Cwd       string
	CreatedAt time.Time
	UpdatedAt time.Time
	TokensIn  int
	TokensOut int

	// Messages to export.
	Messages []session.Encoded
}

// RenderMarkdown produces a self-contained Markdown document with
// YAML-like metadata header and full conversation transcript.
func RenderMarkdown(opts Options) string {
	var b strings.Builder

	// Header.
	b.WriteString("# SuperCli Session\n\n")
	b.WriteString("| Field | Value |\n")
	b.WriteString("|-------|-------|\n")
	b.WriteString(fmt.Sprintf("| ID | `%s` |\n", opts.ID))
	if opts.Title != "" {
		b.WriteString(fmt.Sprintf("| Title | %s |\n", opts.Title))
	}
	b.WriteString(fmt.Sprintf("| Model | %s |\n", opts.Model))
	if opts.Cwd != "" {
		b.WriteString(fmt.Sprintf("| Directory | `%s` |\n", opts.Cwd))
	}
	b.WriteString(fmt.Sprintf("| Created | %s |\n", opts.CreatedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("| Updated | %s |\n", opts.UpdatedAt.Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("| Tokens (in/out) | %d / %d |\n", opts.TokensIn, opts.TokensOut))
	b.WriteString(fmt.Sprintf("| Messages | %d |\n", len(opts.Messages)))
	b.WriteString(fmt.Sprintf("| Exported | %s |\n\n", time.Now().Format(time.RFC3339)))

	// Transcript.
	b.WriteString("---\n\n")
	for _, msg := range opts.Messages {
		role := msg.Role
		content := msg.Content
		if content == "" {
			continue
		}
		switch role {
		case "system":
			b.WriteString(fmt.Sprintf("## System\n\n> %s\n\n", indentBlock(content, "> ")))
		case "user":
			b.WriteString(fmt.Sprintf("## User\n\n%s\n\n", content))
		case "assistant":
			b.WriteString(fmt.Sprintf("## Assistant\n\n%s\n\n", content))
		case "tool":
			name := msg.Name
			if name == "" {
				name = "tool"
			}
			b.WriteString(fmt.Sprintf("### %s result\n\n```\n%s\n```\n\n", name, content))
		}
	}

	return b.String()
}

// DefaultFilename generates a filename from session metadata.
func DefaultFilename(opts Options) string {
	title := opts.ID
	if len(title) > 8 {
		title = title[:8]
	}
	if opts.Title != "" {
		slug := slugify(opts.Title)
		if len(slug) > 40 {
			slug = slug[:40]
		}
		title = slug + "-" + title
	}
	return "supercli-" + title + ".md"
}

func indentBlock(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		if r == ' ' || r == '/' || r == '\\' {
			return '-'
		}
		return -1
	}, s)
	// Collapse multiple dashes.
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	return s
}
