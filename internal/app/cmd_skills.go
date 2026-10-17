package app

import (
	"context"
	"fmt"
	"strings"

	"supercli/internal/system/uilang"
	"supercli/internal/tools"
	"supercli/internal/ui/tui"
)

// /skills browses metadata only: no model call, guidance loading or extraction.
func skillsCommand(discoverer *tools.Discoverer, language string) tui.SlashHandler {
	return func(_ context.Context, args string) (string, error) {
		items, total, err := discoverer.List(strings.TrimSpace(args), 0, 50)
		if err != nil {
			return "", fmt.Errorf("skills: %w", err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# %s · %d/%d\n\n", uilang.Text(language, "skills.title"), len(items), total)
		if len(items) == 0 {
			b.WriteString(uilang.Text(language, "skills.empty") + "\n\n")
		}
		for _, skill := range items {
			source := skill.Source
			if source == "" {
				switch {
				case skill.Priority >= 90:
					source = "project"
				case skill.Priority >= 40:
					source = "user"
				default:
					source = "builtin"
				}
			}
			label := uilang.Text(language, "skills.source."+source)
			if label == "skills.source."+source {
				label = source
			}
			fmt.Fprintf(&b, "- **%s** · %s\n", skill.Name, label)
			if description := strings.Join(strings.Fields(skill.Description), " "); description != "" {
				fmt.Fprintf(&b, "  %s\n", description)
			}
		}
		b.WriteString("\n" + uilang.Text(language, "skills.hint"))
		return b.String(), nil
	}
}
