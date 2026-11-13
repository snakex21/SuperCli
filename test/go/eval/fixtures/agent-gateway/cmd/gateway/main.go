package main

import (
	"log"
	"net/http"

	"gateway/internal/config"
	ghttp "gateway/internal/http"
)

func main() {
	cfg := config.Load()
	log.Fatal(http.ListenAndServe(cfg.Addr, ghttp.NewRouter(cfg)))
}
