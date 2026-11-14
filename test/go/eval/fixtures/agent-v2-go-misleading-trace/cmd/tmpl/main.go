// Command tmpl renders one template from stdin.
package main

import (
	"fmt"
	"io"
	"os"

	"tmplsvc/config"
	"tmplsvc/render"
)

func main() {
	data, _ := io.ReadAll(os.Stdin)
	page := "index"
	if len(os.Args) > 1 {
		page = os.Args[1]
	}
	fmt.Print(render.Render(config.Merge(config.Base(), config.Config{}), page, string(data)))
}
