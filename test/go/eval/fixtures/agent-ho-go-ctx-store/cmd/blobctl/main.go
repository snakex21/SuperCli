// Command blobctl prints a blob from a disk store.
package main

import (
	"fmt"
	"os"

	"blobhub/storage/disk"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: blobctl <dir> <key>")
		os.Exit(2)
	}
	b, err := disk.Store{Dir: os.Args[1]}.Get(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Stdout.Write(b)
}
