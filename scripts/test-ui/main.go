// Command test-ui runs native ESM UI tests in an isolated development browser.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"supercli/internal/uitest"
)

func main() {
	os.Exit(run())
}

func run() int {
	filter := flag.String("filter", "", "substring of test/ui/*.test.js filenames")
	browser := flag.String("browser", "", "dev-only Chrome/Chromium/Edge executable (or SUPERCLI_TEST_BROWSER)")
	timeout := flag.Duration("timeout", 7*time.Minute, "deadline for the entire UI suite")
	flag.Parse()
	if flag.NArg() != 0 || *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/test-ui [-filter substring] [-browser executable] [-timeout 7m]")
		return 2
	}
	ctx, interrupt := signal.NotifyContext(context.Background(), os.Interrupt)
	defer interrupt()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	result, err := uitest.Run(ctx, uitest.Options{Filter: *filter, Browser: *browser})
	result.Log(func(format string, args ...any) { fmt.Printf(format+"\n", args...) })
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
