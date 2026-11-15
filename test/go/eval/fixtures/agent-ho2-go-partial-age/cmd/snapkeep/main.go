// Command snapkeep lists and prunes snapshot archives.
package main

import (
	"fmt"
	"os"
	"time"

	"snapkeep/cli"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "snapkeep:", err)
		os.Exit(1)
	}
}
