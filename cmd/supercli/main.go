package main

import (
	"supercli/internal/app"
	_ "supercli/internal/system/termux"
)

func main() {
	defer setupConsole()()
	app.Main()
}
