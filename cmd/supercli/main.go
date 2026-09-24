package main

import "supercli/internal/app"

func main() {
	defer setupConsole()()
	app.Main()
}
