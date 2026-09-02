package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"supercli/internal/tools"
)

func main() {
	one := flag.String("json", "", "single thunderbird_mail JSON request; otherwise read one request per stdin line")
	timeout := flag.Duration("timeout", 3*time.Minute, "timeout per request")
	flag.Parse()

	requests := []string{}
	if strings.TrimSpace(*one) != "" {
		requests = append(requests, *one)
	} else {
		scanner := bufio.NewScanner(os.Stdin)
		buffer := make([]byte, 64*1024)
		scanner.Buffer(buffer, 4*1024*1024)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				requests = append(requests, line)
			}
		}
		if err := scanner.Err(); err != nil {
			fatalf("read requests: %v", err)
		}
	}
	if len(requests) == 0 {
		fatalf("provide -json or JSON lines on stdin")
	}

	fn := tools.NewThunderbirdMail().Spec().Fn
	for _, request := range requests {
		if !json.Valid([]byte(request)) {
			fatalf("invalid JSON request: %s", request)
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		result, err := fn(ctx, json.RawMessage(request))
		cancel()
		if err != nil {
			fatalf("execute: %v", err)
		}
		if result.Err != nil {
			fatalf("tool: %v", result.Err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(result.Text)); err != nil {
			fatalf("compact tool response: %v", err)
		}
		fmt.Println(compact.String())
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FATAL "+format+"\n", args...)
	os.Exit(1)
}
