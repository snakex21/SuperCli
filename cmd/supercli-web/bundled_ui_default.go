package main

import "io/fs"

func bundledUI() (fs.FS, string, string) {
	return nil, "SuperCli", "supercli"
}
