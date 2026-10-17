package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type locale struct {
	Code  string
	Label string
}

// JSON object order is part of the navigation and language inventory contract.
// A map would silently discard both that order and duplicate locale keys.
type localeList []locale

func (locales *localeList) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("locales must be an ordered object")
	}
	*locales = nil
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		var label string
		if err := decoder.Decode(&label); err != nil {
			return err
		}
		*locales = append(*locales, locale{Code: key.(string), Label: label})
	}
	_, err = decoder.Token()
	return err
}

type block struct {
	ID      string     `json:"-"`
	Type    string     `json:"type"`
	Text    string     `json:"text"`
	Key     string     `json:"key"`
	Lang    string     `json:"lang"`
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

func (b *block) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &b.ID)
	}
	type object block
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*object)(b))
}

type source struct {
	Locales localeList        `json:"locales"`
	Links   map[string]string `json:"links"`
	Text    [][]string        `json:"text"`
	Blocks  []block           `json:"blocks"`
}

type catalog struct {
	root         string
	source       source
	translations map[string][]string
}

func loadCatalog(root string) (*catalog, error) {
	c := &catalog{root: root, translations: make(map[string][]string)}
	if err := readJSON(filepath.Join(c.readmeDir(), "source.json"), &c.source); err != nil {
		return nil, err
	}
	for _, locale := range c.source.Locales {
		if locale.Code == "en" {
			continue
		}
		// Locale codes become filenames; reject unexpected paths before reading.
		if !localeCodePattern.MatchString(locale.Code) {
			return nil, fmt.Errorf("invalid locale code %q", locale.Code)
		}
		var values []string
		file := filepath.Join(c.readmeDir(), "translations", locale.Code+".json")
		if err := readJSON(file, &values); err != nil {
			return nil, err
		}
		c.translations[locale.Code] = values
	}
	return c, nil
}

func (c *catalog) readmeDir() string {
	return filepath.Join(c.root, "docs", "readme")
}

func (c *catalog) ids() ([]string, error) {
	ids := make([]string, 0, len(c.source.Text))
	for i, unit := range c.source.Text {
		if len(unit) != 2 || unit[0] == "" {
			return nil, fmt.Errorf("English content unit %d: expected [nonempty ID, text]", i+1)
		}
		ids = append(ids, unit[0])
	}
	return ids, nil
}

func (c *catalog) valuesFor(code string) (map[string]string, error) {
	ids, err := c.ids()
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(ids))
	if code == "en" {
		for _, unit := range c.source.Text {
			values[unit[0]] = unit[1]
		}
		return values, nil
	}
	translated, ok := c.translations[code]
	if !ok || len(translated) != len(ids) {
		return nil, fmt.Errorf("%s: expected %d translated units, got %d", code, len(ids), len(translated))
	}
	for i, id := range ids {
		values[id] = translated[i]
	}
	return values, nil
}

func isRemote(target string) bool {
	return strings.HasPrefix(target, "http:") || strings.HasPrefix(target, "https:")
}

func (c *catalog) render(code, file string) (string, error) {
	values, err := c.valuesFor(code)
	if err != nil {
		return "", err
	}
	rel := func(target string) (string, error) {
		if isRemote(target) {
			return target, nil
		}
		path, err := filepath.Rel(filepath.Dir(file), filepath.Join(c.root, filepath.FromSlash(target)))
		return filepath.ToSlash(path), err
	}
	text := func(id string) (string, error) {
		value, ok := values[id]
		if !ok {
			return "", fmt.Errorf("unknown content unit %q", id)
		}
		var linkErr error
		value = placeholderPattern.ReplaceAllStringFunc(value, func(placeholder string) string {
			key := placeholder[1 : len(placeholder)-1]
			target, ok := c.source.Links[key]
			if !ok || target == "" {
				linkErr = fmt.Errorf("unknown link %q in %s", key, id)
				return placeholder
			}
			path, err := rel(target)
			if err != nil {
				linkErr = err
			}
			return path
		})
		return strings.ReplaceAll(value, "|", "\\|"), linkErr
	}
	var nav []string
	for _, locale := range c.source.Locales {
		target, err := rel("docs/readme/" + locale.Code + ".md")
		if err != nil {
			return "", err
		}
		nav = append(nav, fmt.Sprintf("[%s](%s)", locale.Label, target))
	}
	var body []string
	for _, block := range c.source.Blocks {
		var part string
		if block.ID != "" {
			value, err := text(block.ID)
			if err != nil {
				return "", err
			}
			prefix := ""
			if strings.HasPrefix(block.ID, "h.") {
				prefix = "## "
			}
			part = "<!-- readme-unit:" + block.ID + " -->\n" + prefix + value
		} else {
			switch block.Type {
			case "title":
				part = "# " + block.Text
			case "reference":
				target, ok := c.source.Links[block.Key]
				if !ok || target == "" {
					return "", fmt.Errorf("unknown reference link %q", block.Key)
				}
				path, err := rel(target)
				if err != nil {
					return "", err
				}
				part = "[" + target + "](" + path + ")"
			case "code":
				part = "```" + block.Lang + "\n" + block.Text + "\n```"
			case "table":
				var headers, rows []string
				mapped := append([]string(nil), block.Headers...)
				for _, id := range block.Headers {
					value, err := text(id)
					if err != nil {
						return "", err
					}
					headers = append(headers, value)
				}
				for _, row := range block.Rows {
					if len(row) != 2 {
						return "", fmt.Errorf("table row must contain a literal and content ID")
					}
					value, err := text(row[1])
					if err != nil {
						return "", err
					}
					mapped = append(mapped, row[1])
					rows = append(rows, "| "+strings.ReplaceAll(row[0], "|", "\\|")+" | "+value+" |")
				}
				part = "<!-- readme-unit:" + strings.Join(mapped, ",") + " -->\n| " + strings.Join(headers, " | ") + " |\n| --- | --- |\n" + strings.Join(rows, "\n")
			default:
				return "", fmt.Errorf("unknown block type %q", block.Type)
			}
		}
		body = append(body, part)
	}
	return strings.Join(nav, " · ") + "\n\n<!-- Generated from docs/readme/source.json and translations/*.json; edit those files, then run go run ./scripts/readme -generate. -->\n\n" + strings.Join(body, "\n\n") + "\n", nil
}

func (c *catalog) generate() error {
	if err := c.checkSources(); err != nil {
		return err
	}
	// Render everything before writing, so a bad source cannot leave a partial set.
	type document struct{ file, content string }
	var documents []document
	for _, locale := range c.source.Locales {
		file := filepath.Join(c.readmeDir(), locale.Code+".md")
		content, err := c.render(locale.Code, file)
		if err != nil {
			return err
		}
		documents = append(documents, document{file, content})
	}
	file := filepath.Join(c.root, "README.md")
	content, err := c.render("en", file)
	if err != nil {
		return err
	}
	documents = append(documents, document{file, content})
	for _, doc := range documents {
		if err := os.WriteFile(doc.file, []byte(doc.content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
