//go:build nestcafe

package main

import (
	"embed"
	"io/fs"
)

//go:embed nestcafe-ui/*
var nestCafeAssets embed.FS

func bundledUI() (fs.FS, string, bool) {
	sub, err := fs.Sub(nestCafeAssets, "nestcafe-ui")
	if err != nil {
		panic(err)
	}
	return sub, "NestCafe", true
}
