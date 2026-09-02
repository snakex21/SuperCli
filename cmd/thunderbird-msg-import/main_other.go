//go:build !windows

package main

import "fmt"

func main() {
	fmt.Println("thunderbird-msg-import requires Windows and classic Outlook")
}
