//go:build !nestcafe

package main

import "io/fs"

func bundledUI() (fs.FS, string, bool) {
	return nil, "SuperCli", false
}
