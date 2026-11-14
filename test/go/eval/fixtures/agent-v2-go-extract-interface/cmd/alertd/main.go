// Command alertd wires the mail client into the alerting service.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"alerts/internal/service"
	"alerts/internal/smtp"
)

func main() {
	c, err := smtp.Dial("localhost:25")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer c.Close()
	c.SetTimeout(5 * time.Second)
	a := service.NewAlerter(c, "ops@example.com", 2)
	d := service.NewDigest(c, "ops@example.com")
	ctx := context.Background()
	_ = a.Alert(ctx, 3, "disk almost full")
	d.Add("nightly job ok")
	_ = d.Flush(ctx)
	fmt.Println("sent:", c.Stats().Sent)
}
