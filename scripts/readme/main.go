// Command readme generates and checks the shared README language editions.
// It uses only the Go standard library and runs from anywhere in the repository.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "readme:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("readme", flag.ContinueOnError)
	flags.SetOutput(stderr)
	generate := flags.Bool("generate", false, "regenerate README.md and all 27 language editions")
	check := flags.Bool("check", false, "check sources and all generated README files without writing")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: go run ./scripts/readme -generate | -check")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *generate == *check {
		flags.Usage()
		return errors.New("choose exactly one of -generate or -check, with no positional arguments")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := findRoot(cwd)
	if err != nil {
		return err
	}
	catalog, err := loadCatalog(root)
	if err != nil {
		return err
	}
	if *generate {
		if err := catalog.generate(); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Generated README.md and %d language editions (%d files).\n", len(catalog.source.Locales), len(catalog.source.Locales)+1)
		return nil
	}
	if err := catalog.check(); err != nil {
		return err
	}
	english, err := catalog.render("en", filepath.Join(root, "README.md"))
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "README parity passed: %d languages, %d translated units each, %d identical code blocks, all link targets valid (%d files).\n", len(catalog.source.Locales), len(catalog.source.Text), len(codeBlocks(english)), len(catalog.source.Locales)+1)
	return nil
}

func findRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "docs", "readme", "source.json")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("cannot find go.mod and docs/readme/source.json above %s", start)
		}
		dir = parent
	}
}

func readJSON(file string, value any) error {
	data, err := os.Open(file)
	if err != nil {
		return err
	}
	defer data.Close()
	decoder := json.NewDecoder(data)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fmt.Errorf("%s: %w", file, err)
	}
	return nil
}
