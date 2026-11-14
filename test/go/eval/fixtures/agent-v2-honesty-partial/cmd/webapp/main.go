package main

import (
	"log"
	nethttp "net/http"

	"webapp/internal/http"
)

func main() {
	log.Fatal(http.New(":8080", nethttp.NotFoundHandler()).ListenAndServe())
}
