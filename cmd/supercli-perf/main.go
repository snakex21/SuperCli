package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"supercli/internal/perfbench"
)

func main() {
	name := "supercli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := flag.String("binary", filepath.Join(".", name), "built SuperCli binary")
	iterations := flag.Int("iterations", 5, "measured samples")
	warmup := flag.Int("warmup", 1, "discarded warmup samples")
	cold := flag.Float64("cold-start-warn-ms", perfbench.DefaultThresholds.ColdStartP95Ms, "cold start p95 warning threshold")
	first := flag.Float64("first-output-warn-ms", perfbench.DefaultThresholds.FirstOutputP95Ms, "first output p95 warning threshold")
	rss := flag.Float64("peak-rss-warn-mb", perfbench.DefaultThresholds.PeakRSSMaxMB, "peak RSS warning threshold")
	output := flag.String("output", "", "optional JSON report path")
	jsonOnly := flag.Bool("json", false, "print JSON")
	fail := flag.Bool("fail-on-warning", false, "exit 1 on threshold warning")
	flag.Parse()
	report, err := perfbench.Run(context.Background(), perfbench.Options{Command: []string{*binary, "--version"}, Iterations: *iterations, Warmup: *warmup, Thresholds: perfbench.Thresholds{ColdStartP95Ms: *cold, FirstOutputP95Ms: *first, PeakRSSMaxMB: *rss}})
	if err != nil {
		fmt.Fprintln(os.Stderr, "supercli-perf:", err)
		os.Exit(1)
	}
	var buf bytes.Buffer
	_ = perfbench.WriteJSON(&buf, report)
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(*output, buf.Bytes(), 0o644); err != nil {
			panic(err)
		}
	}
	if *jsonOnly {
		fmt.Print(buf.String())
	} else {
		fmt.Println(perfbench.FormatSummary(report))
	}
	if *fail && len(report.Warnings) > 0 {
		os.Exit(1)
	}
}
