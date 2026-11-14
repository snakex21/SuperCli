// Command shopctl is a small admin CLI.
package main

import (
	"fmt"
	"os"

	"shopcore/internal/money"
)

func main() {
	if len(os.Args) != 3 || os.Args[1] != "parse" {
		fmt.Fprintln(os.Stderr, "usage: shopctl parse <amount>")
		os.Exit(2)
	}
	a, err := money.Parse(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(a)
}
