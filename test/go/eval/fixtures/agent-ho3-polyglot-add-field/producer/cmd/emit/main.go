// Command emit reads a JSON array of orders and prints one OrderEvent JSON
// line per order (used by contract/check_contract.py).
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"polyglot/producer/event"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: emit ORDERS.json")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var orders []event.Order
	if err := json.Unmarshal(data, &orders); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, o := range orders {
		e, err := event.FromOrder(o)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		b, _ := event.Encode(e)
		os.Stdout.Write(b)
	}
}
