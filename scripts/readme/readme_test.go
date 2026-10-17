package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func testCatalog(t *testing.T) *catalog {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := findRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func canonicalDocument(t *testing.T) (*catalog, string, string) {
	t.Helper()
	c := testCatalog(t)
	file := filepath.Join(c.readmeDir(), "en.md")
	content, err := c.render("en", file)
	if err != nil {
		t.Fatal(err)
	}
	return c, file, content
}

func rejectsDocument(t *testing.T, mutate func(string) string, reason string) {
	t.Helper()
	c, file, original := canonicalDocument(t)
	changed := mutate(original)
	if changed == original {
		t.Fatal("mutation did not change the document")
	}
	if err := c.checkDocument("en", changed, file); err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("expected rejection containing %q, got %v", reason, err)
	}
}

// These eleven tests retain every case from the former check.test.cjs suite.
func TestTranslationsPreserveTechnicalLiteralsAndDestinations(t *testing.T) {
	if err := testCatalog(t).checkSources(); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalDocumentPasses(t *testing.T) {
	c, file, original := canonicalDocument(t)
	if err := c.checkDocument("en", original, file); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsCheckoutLineEndingsPreserveDocumentParity(t *testing.T) {
	c, file, original := canonicalDocument(t)
	if err := c.checkDocument("en", strings.ReplaceAll(original, "\n", "\r\n"), file); err != nil {
		t.Fatal(err)
	}
}

func TestCRLFRejectsChangedCodeExample(t *testing.T) {
	rejectsDocument(t, func(value string) string {
		return strings.ReplaceAll(strings.Replace(value, "go test ./...", "go test ./cmd/...", 1), "\n", "\r\n")
	}, "code examples differ")
}

func TestRejectsMissingParagraph(t *testing.T) {
	rejectsDocument(t, func(value string) string {
		return regexp.MustCompile(`<!-- readme-unit:intro -->\n[^\n]+\n\n`).ReplaceAllString(value, "")
	}, "content units")
}

func TestRejectsDuplicateUnit(t *testing.T) {
	rejectsDocument(t, func(value string) string {
		return value + "\n<!-- readme-unit:intro -->\nRepeated content.\n"
	}, "content units")
}

func TestRejectsUnmappedParagraph(t *testing.T) {
	rejectsDocument(t, func(value string) string {
		return value + "\nUntranslated extra paragraph.\n"
	}, "generated text differs")
}

func TestRejectsChangedHeadingLevel(t *testing.T) {
	rejectsDocument(t, func(value string) string {
		return strings.Replace(value, "## Get started", "### Get started", 1)
	}, "heading structure differs")
}

func TestRejectsChangedCodeExample(t *testing.T) {
	rejectsDocument(t, func(value string) string {
		return strings.Replace(value, "go test ./...", "go test ./cmd/...", 1)
	}, "code examples differ")
}

func TestRejectsWrongLinkDestination(t *testing.T) {
	rejectsDocument(t, func(value string) string {
		return strings.Replace(value, "(../quickstart.md)", "(../architecture.md)", 1)
	}, "resolved links differ")
}

func TestRejectsChangedCommandTableCell(t *testing.T) {
	rejectsDocument(t, func(value string) string {
		return strings.Replace(value, "`/resume`, `/export`", "`/resume`, `/quit`", 1)
	}, "generated text differs")
}

func TestRejectsInvalidSources(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*catalog)
		reason string
	}{
		{"reordered languages", func(c *catalog) {
			c.source.Locales[0], c.source.Locales[1] = c.source.Locales[1], c.source.Locales[0]
		}, "language inventory and order"},
		{"missing language", func(c *catalog) {
			c.source.Locales = c.source.Locales[:26]
		}, "language inventory and order"},
		{"duplicate ID", func(c *catalog) {
			c.source.Text[1][0] = c.source.Text[0][0]
		}, "unique English content IDs"},
		{"malformed English unit", func(c *catalog) {
			c.source.Text[0] = []string{"intro"}
		}, "expected [nonempty ID, text]"},
		{"empty English unit", func(c *catalog) {
			c.source.Text[0][1] = " "
		}, "single-line English unit"},
		{"missing translation", func(c *catalog) {
			delete(c.translations, "pl")
		}, "complete translation inventory"},
		{"extra translation", func(c *catalog) {
			c.translations["xx"] = c.translations["pl"]
		}, "complete translation inventory"},
		{"missing translated unit", func(c *catalog) {
			c.translations["pl"] = c.translations["pl"][1:]
		}, "translated units"},
		{"empty translation", func(c *catalog) {
			c.translations["pl"][0] = " "
		}, "empty translation"},
		{"multiline translation", func(c *catalog) {
			c.translations["pl"][0] += "\nExtra paragraph."
		}, "one mapped unit"},
		{"carriage return in translation", func(c *catalog) {
			c.translations["pl"][0] += "\rExtra paragraph."
		}, "one mapped unit"},
		{"changed technical literal", func(c *catalog) {
			c.translations["pl"][1] = strings.Replace(c.translations["pl"][1], "`1.0.14`", "`1.0.15`", 1)
		}, "technical literals differ"},
		{"changed placeholder", func(c *catalog) {
			c.translations["pl"][1] = strings.Replace(c.translations["pl"][1], "{releases}", "{quickstart}", 1)
		}, "link destinations differ"},
		{"changed numeric fact", func(c *catalog) {
			c.translations["pl"][0] += " 28"
		}, "numeric facts differ"},
		{"untranslated paragraph", func(c *catalog) {
			c.translations["pl"][0] = c.source.Text[0][1]
		}, "untranslated English paragraph"},
		{"missing mapped block", func(c *catalog) {
			c.source.Blocks = append(c.source.Blocks[:1], c.source.Blocks[2:]...)
		}, "source blocks"},
		{"unknown mapped unit", func(c *catalog) {
			c.source.Blocks[1].ID = "missing"
		}, "unknown content unit"},
		{"unknown block type", func(c *catalog) {
			c.source.Blocks[0].Type = "unknown"
		}, "unknown block type"},
		{"unknown link", func(c *catalog) {
			delete(c.source.Links, "releases")
		}, "unknown link"},
		{"unknown reference", func(c *catalog) {
			delete(c.source.Links, "releasing")
		}, "unknown reference link"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testCatalog(t)
			tc.mutate(c)
			if err := c.checkSources(); err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("expected rejection containing %q, got %v", tc.reason, err)
			}
		})
	}
}

func writeTestFile(t *testing.T, file string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Use temporary link-target stubs, not application data, browsers or binaries.
func fixtureCatalog(t *testing.T) *catalog {
	t.Helper()
	c := testCatalog(t)
	oldRoot := c.root
	c.root = t.TempDir()
	files := []string{"go.mod", "docs/readme/source.json"}
	for _, code := range expectedLocales[1:] {
		files = append(files, "docs/readme/translations/"+code+".json")
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(oldRoot, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(c.root, filepath.FromSlash(file)), data)
	}
	for _, target := range c.source.Links {
		if !isRemote(target) {
			writeTestFile(t, filepath.Join(c.root, filepath.FromSlash(target)), nil)
		}
	}
	return c
}

func TestGenerateAndCheckAllEditions(t *testing.T) {
	c := fixtureCatalog(t)
	if err := c.generate(); err != nil {
		t.Fatal(err)
	}
	if err := c.check(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(c.readmeDir(), "*.md"))
	if err != nil || len(files) != 27 {
		t.Fatalf("expected 27 language editions, got %d: %v", len(files), err)
	}
	files = append(files, filepath.Join(c.root, "README.md"))
	before := make(map[string]string)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		before[file] = string(data)
	}
	root := before[filepath.Join(c.root, "README.md")]
	english := before[filepath.Join(c.readmeDir(), "en.md")]
	if !strings.Contains(root, "[English](docs/readme/en.md)") || !strings.Contains(root, "(docs/quickstart.md)") || !strings.Contains(english, "[English](en.md)") || !strings.Contains(english, "(../quickstart.md)") {
		t.Fatal("root and localized README paths must resolve from their own directories")
	}
	if !strings.Contains(english, "`/update check\\|download\\|install`") {
		t.Fatal("command table pipes must stay escaped")
	}
	if err := c.generate(); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil || string(data) != before[file] {
			t.Fatalf("generation is not deterministic for %s: %v", file, err)
		}
	}
	for _, file := range files {
		writeTestFile(t, file, []byte(strings.ReplaceAll(before[file], "\n", "\r\n")))
	}
	if err := c.check(); err != nil {
		t.Fatalf("all 28 CRLF documents must pass: %v", err)
	}
	file := filepath.Join(c.readmeDir(), "pl.md")
	writeTestFile(t, file, []byte(before[file]+"\nUnmapped paragraph.\n"))
	if err := c.check(); err == nil || !strings.Contains(err.Error(), "generated text differs") {
		t.Fatalf("changed translation was not rejected: %v", err)
	}
}

func TestRejectsMissingLinkTargets(t *testing.T) {
	c := fixtureCatalog(t)
	c.source.Links["quickstart"] = "docs/does-not-exist.md"
	file := filepath.Join(c.readmeDir(), "en.md")
	content, err := c.render("en", file)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.checkDocument("en", content, file); err == nil || !strings.Contains(err.Error(), "missing linked file") {
		t.Fatalf("missing local destination was not rejected: %v", err)
	}
}

func TestJSONContracts(t *testing.T) {
	t.Run("locale order and duplicate keys are retained", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "locales.json")
		writeTestFile(t, file, []byte(`{"en":"English","pl":"Polski","en":"Duplicate"}`))
		var locales localeList
		if err := readJSON(file, &locales); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(locales, localeList{{"en", "English"}, {"pl", "Polski"}, {"en", "Duplicate"}}) {
			t.Fatalf("locale order or duplicates lost: %v", locales)
		}
	})
	for _, data := range []string{
		`{"unknown":true}`,
		`{"blocks":[{"type":"title","unknown":true}]}`,
		`{"locales":[]}`,
		`{"locales":{"en":123}}`,
		`{} {}`,
		`{"text":[["intro",123]]}`,
		`{`,
	} {
		t.Run(data, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "source.json")
			writeTestFile(t, file, []byte(data))
			if err := readJSON(file, new(source)); err == nil {
				t.Fatal("invalid source JSON was accepted")
			}
		})
	}
	t.Run("translation strings required", func(t *testing.T) {
		c := fixtureCatalog(t)
		writeTestFile(t, filepath.Join(c.readmeDir(), "translations", "pl.json"), []byte(`[123]`))
		if _, err := loadCatalog(c.root); err == nil {
			t.Fatal("non-string translation was accepted")
		}
	})
}

func TestCLI(t *testing.T) {
	c := fixtureCatalog(t)
	t.Chdir(c.root)
	var stdout, stderr bytes.Buffer
	for _, args := range [][]string{nil, {"-generate", "-check"}, {"-check", "extra"}, {"-unknown"}} {
		if err := run(args, &stdout, &stderr); err == nil {
			t.Fatalf("invalid CLI arguments accepted: %v", args)
		}
	}
	if err := run([]string{"-help"}, &stdout, &stderr); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help returned %v", err)
	}
	if err := run([]string{"-generate"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "27 language editions (28 files)") {
		t.Fatalf("missing generation summary: %s", stdout.String())
	}
	stdout.Reset()
	t.Chdir(filepath.Join(c.readmeDir(), "translations"))
	if err := run([]string{"-check"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "27 languages, 57 translated units each, 9 identical code blocks") {
		t.Fatalf("missing parity summary: %s", stdout.String())
	}
}

func TestCheckedInDocumentsPass(t *testing.T) {
	if err := testCatalog(t).check(); err != nil {
		t.Fatal(err)
	}
}
