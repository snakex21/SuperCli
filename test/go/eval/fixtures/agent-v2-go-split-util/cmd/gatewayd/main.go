// Command gatewayd runs the gateway.
package main

import (
	"fmt"
	"os"

	"gateway/internal/proxy"
	"gateway/internal/routes"
	"gateway/internal/util"
)

func main() {
	to, err := proxy.ParseTimeouts("5", "30s")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	r := routes.New("Health Check", "self:9000")
	fmt.Println(util.Truncate(r.Path, 40), to.Dial)
}
