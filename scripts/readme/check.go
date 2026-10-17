package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"
)

var expectedLocales = strings.Fields("en bg cs da de el es et fi fr hr hu it lt lv nb nl pl pt-BR ro ru sk sl sr-Latn sv tr uk")

var (
	localeCodePattern  = regexp.MustCompile(`^[a-z]{2}(?:-[A-Za-z]+)?$`)
	literalPattern     = regexp.MustCompile("`([^`]+)`")
	placeholderPattern = regexp.MustCompile(`\{([a-z]+)\}`)
	numberPattern      = regexp.MustCompile(`\b\d+(?:\.\d+)*\b`)
	unitPattern        = regexp.MustCompile(`<!-- readme-unit:([^>]+) -->`)
	codePattern        = regexp.MustCompile("(?m)^```([^\n]*)\n([\\s\\S]*?)^```$")
	headingPattern     = regexp.MustCompile(`^(#{1,6}) `)
	linkPattern        = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
)

func captures(pattern *regexp.Regexp, value string, group int) []string {
	var result []string
	for _, match := range pattern.FindAllStringSubmatch(value, -1) {
		result = append(result, match[group])
	}
	return result
}

func unitIDs(value string) []string {
	var ids []string
	for _, group := range captures(unitPattern, value, 1) {
		ids = append(ids, strings.Split(strings.TrimSpace(group), ",")...)
	}
	return ids
}

func codeBlocks(value string) [][2]string {
	var blocks [][2]string
	for _, match := range codePattern.FindAllStringSubmatch(value, -1) {
		blocks = append(blocks, [2]string{match[1], match[2]})
	}
	return blocks
}

func headings(value string) []string {
	value = codePattern.ReplaceAllString(value, "")
	var levels []string
	for _, line := range strings.Split(value, "\n") {
		if match := headingPattern.FindStringSubmatch(line); match != nil {
			levels = append(levels, match[1])
		}
	}
	return levels
}

func (c *catalog) links(value, file string) ([]string, error) {
	var links []string
	for _, target := range captures(linkPattern, value, 1) {
		if isRemote(target) {
			links = append(links, target)
			continue
		}
		resolved := filepath.FromSlash(target)
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(file), resolved)
		}
		if _, err := os.Stat(resolved); err != nil {
			return nil, fmt.Errorf("%s: missing linked file %s: %w", file, target, err)
		}
		rel, err := filepath.Rel(c.root, resolved)
		if err != nil {
			return nil, err
		}
		links = append(links, filepath.ToSlash(rel))
	}
	return links, nil
}

func (c *catalog) checkSources() error {
	var codes []string
	for _, locale := range c.source.Locales {
		codes = append(codes, locale.Code)
		if strings.TrimSpace(locale.Label) == "" {
			return fmt.Errorf("%s: native language label required", locale.Code)
		}
	}
	if !slices.Equal(codes, expectedLocales) {
		return fmt.Errorf("exact language inventory and order required: got %v, want %v", codes, expectedLocales)
	}
	ids, err := c.ids()
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	for i, id := range ids {
		if seen[id] {
			return fmt.Errorf("unique English content IDs required: duplicate %q", id)
		}
		seen[id] = true
		value := c.source.Text[i][1]
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("en/%s: nonempty, single-line English unit required", id)
		}
	}
	if len(c.translations) != len(expectedLocales)-1 {
		return fmt.Errorf("complete translation inventory required: expected %d languages, got %d", len(expectedLocales)-1, len(c.translations))
	}
	for _, code := range expectedLocales[1:] {
		values, err := c.valuesFor(code)
		if err != nil {
			return err
		}
		for _, unit := range c.source.Text {
			id, english := unit[0], unit[1]
			value := values[id]
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s/%s: empty translation", code, id)
			}
			if strings.ContainsAny(value, "\r\n") {
				return fmt.Errorf("%s/%s: each paragraph must remain one mapped unit", code, id)
			}
			for _, rule := range []struct {
				pattern *regexp.Regexp
				group   int
				name    string
			}{
				{literalPattern, 1, "technical literals"},
				{placeholderPattern, 1, "link destinations"},
				{numberPattern, 0, "numeric facts"},
			} {
				if !slices.Equal(captures(rule.pattern, value, rule.group), captures(rule.pattern, english, rule.group)) {
					return fmt.Errorf("%s/%s: %s differ", code, id, rule.name)
				}
			}
			// Match the previous checker's JavaScript string-length threshold.
			if len(utf16.Encode([]rune(english))) > 60 && value == english {
				return fmt.Errorf("%s/%s: untranslated English paragraph", code, id)
			}
		}
	}
	// Even the canonical render must cover the source IDs exactly once, in order.
	english, err := c.render("en", filepath.Join(c.readmeDir(), "en.md"))
	if err != nil {
		return err
	}
	if !slices.Equal(unitIDs(english), ids) {
		return fmt.Errorf("source blocks: missing, duplicate, or reordered content units")
	}
	return nil
}

func (c *catalog) checkDocument(code, content, file string) error {
	// Git may check Markdown out with CRLF on Windows; compare its content.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	englishFile := filepath.Join(c.readmeDir(), "en.md")
	english, err := c.render("en", englishFile)
	if err != nil {
		return err
	}
	ids, err := c.ids()
	if err != nil {
		return err
	}
	if !slices.Equal(unitIDs(content), ids) {
		return fmt.Errorf("%s: missing, duplicate, or reordered content units", code)
	}
	if !slices.Equal(headings(content), headings(english)) {
		return fmt.Errorf("%s: heading structure differs", code)
	}
	if !slices.Equal(codeBlocks(content), codeBlocks(english)) {
		return fmt.Errorf("%s: code examples differ", code)
	}
	actualLinks, err := c.links(content, file)
	if err != nil {
		return err
	}
	expectedLinks, err := c.links(english, englishFile)
	if err != nil {
		return err
	}
	if !slices.Equal(actualLinks, expectedLinks) {
		return fmt.Errorf("%s: resolved links differ", code)
	}
	expected, err := c.render(code, file)
	if err != nil {
		return err
	}
	if content != expected {
		return fmt.Errorf("%s: generated text differs; check unmapped or changed paragraphs and regenerate", code)
	}
	return nil
}

func (c *catalog) check() error {
	if err := c.checkSources(); err != nil {
		return err
	}
	for _, locale := range c.source.Locales {
		file := filepath.Join(c.readmeDir(), locale.Code+".md")
		content, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if err := c.checkDocument(locale.Code, string(content), file); err != nil {
			return err
		}
	}
	file := filepath.Join(c.root, "README.md")
	content, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	return c.checkDocument("en", string(content), file)
}
