package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

// run parses args and writes the greeting to stdout.
func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("greet", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "world", "who to greet")
	excited := fs.Bool("excited", false, "end with an exclamation mark")
	upper := fs.Bool("upper", false, "print the greeting in upper case")
	if err := fs.Parse(args); err != nil {
		return err
	}
	msg := "Hello, " + *name
	if *excited {
		msg += "!"
	} else {
		msg += "."
	}
	if *upper {
		msg = strings.ToUpper(msg)
	}
	fmt.Fprintln(stdout, msg)
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
