package tui

import (
	"os"
	"strings"
)

func buildMentionItems(home string, languages ...string) []autocompleteItem {
	language := "en"
	if len(languages) > 0 {
		language = normalizeLanguage(languages[0])
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	items := make([]autocompleteItem, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		// Skip hidden files and .supercli.
		if strings.HasPrefix(name, ".") {
			continue
		}
		label := name
		desc := ""
		if e.IsDir() {
			label = name + "/"
			desc = textFor(language, "dir", "folder")
		} else {
			// Show file size.
			info, err := e.Info()
			if err == nil {
				desc = humanSize(info.Size())
			}
		}
		items = append(items, autocompleteItem{
			Label:    label,
			Desc:     desc,
			Value:    "@" + label + " ",
			Category: textFor(language, "file", "plik"),
		})
	}
	return items
}

// --- Filtering ---

// filterItems returns items whose Label contains the query (case-insensitive).
func filterItems(items []autocompleteItem, query string) []autocompleteItem {
	if query == "" {
		return items
	}
	q := strings.ToLower(query)
	out := make([]autocompleteItem, 0, len(items))
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Label), q) {
			out = append(out, it)
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, it := range items {
		blob := strings.ToLower(it.Label + " " + it.Desc + " " + it.Hint + " " + it.Category)
		if strings.Contains(strings.ToLower(it.Desc), q) || strings.Contains(strings.ToLower(it.Hint), q) || strings.Contains(strings.ToLower(it.Category), q) || (len(q) >= 3 && fuzzy(blob, q)) {
			out = append(out, it)
		}
	}
	return out
}

// --- Rendering ---

// renderAutocomplete renders the popup as a string (max visible items).
// It's designed to be inserted above the input line in View().
