// Package perfbench measures release-facing CLI overhead. It is linked only
// into cmd/supercli-perf, never into the interactive application.
package perfbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"supercli/internal/system/childproc"
)

type Thresholds struct {
	ColdStartP95Ms   float64 `json:"coldStartP95Ms"`
	FirstOutputP95Ms float64 `json:"firstOutputP95Ms"`
	PeakRSSMaxMB     float64 `json:"peakRssMaxMb"`
}

var DefaultThresholds = Thresholds{ColdStartP95Ms: 300, FirstOutputP95Ms: 250, PeakRSSMaxMB: 128}

type Options struct {
	Command    []string
	Iterations int
	Warmup     int
	Thresholds Thresholds
}

type Stats struct {
	Samples []float64 `json:"samples"`
	Min     float64   `json:"min"`
	Median  float64   `json:"median"`
	Average float64   `json:"average"`
	P95     float64   `json:"p95"`
	Max     float64   `json:"max"`
}

type Metrics struct {
	ColdStartMs    Stats `json:"coldStartMs"`
	FirstOutputMs  Stats `json:"firstOutputMs"`
	ProcessDrainMs Stats `json:"processDrainMs"`
	PeakRSSMB      Stats `json:"peakRssMb"`
}

type Warning struct {
	Metric    string  `json:"metric"`
	Statistic string  `json:"statistic"`
	Unit      string  `json:"unit"`
	Message   string  `json:"message"`
	Observed  float64 `json:"observed"`
	Threshold float64 `json:"threshold"`
}

type Report struct {
	SchemaVersion int        `json:"schemaVersion"`
	Timestamp     string     `json:"timestamp"`
	OS            string     `json:"os"`
	Arch          string     `json:"arch"`
	GoVersion     string     `json:"goVersion"`
	Command       []string   `json:"command"`
	Iterations    int        `json:"iterations"`
	Warmup        int        `json:"warmup"`
	Thresholds    Thresholds `json:"thresholds"`
	Metrics       Metrics    `json:"metrics"`
	Warnings      []Warning  `json:"warnings"`
	DurationMs    int64      `json:"durationMs"`
}

type sample struct{ total, first, drain, peak float64 }

func Run(ctx context.Context, options Options) (Report, error) {
	if len(options.Command) == 0 {
		return Report{}, errors.New("performance command is empty")
	}
	if options.Iterations <= 0 {
		return Report{}, errors.New("iterations must be positive")
	}
	if options.Warmup < 0 {
		return Report{}, errors.New("warmup must not be negative")
	}
	started := time.Now()
	for i := 0; i < options.Warmup; i++ {
		if _, err := measure(ctx, options.Command); err != nil {
			return Report{}, err
		}
	}
	var totals, firsts, drains, peaks []float64
	for i := 0; i < options.Iterations; i++ {
		s, err := measure(ctx, options.Command)
		if err != nil {
			return Report{}, err
		}
		totals = append(totals, s.total)
		firsts = append(firsts, s.first)
		drains = append(drains, s.drain)
		peaks = append(peaks, s.peak)
	}
	report := Report{SchemaVersion: 1, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(), Command: append([]string(nil), options.Command...), Iterations: options.Iterations, Warmup: options.Warmup, Thresholds: options.Thresholds, DurationMs: time.Since(started).Milliseconds()}
	report.Metrics = Metrics{ColdStartMs: Summarize(totals), FirstOutputMs: Summarize(firsts), ProcessDrainMs: Summarize(drains), PeakRSSMB: Summarize(peaks)}
	report.Warnings = warnings(report.Metrics, options.Thresholds)
	return report, nil
}

func measure(ctx context.Context, argv []string) (sample, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	childproc.HideWindow(cmd)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return sample{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return sample{}, err
	}
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return sample{}, fmt.Errorf("start %s: %w", FormatCommand(argv), err)
	}
	var once sync.Once
	var firstAt time.Time
	mark := func() { once.Do(func() { firstAt = time.Now() }) }
	type pipeResult struct {
		data []byte
		err  error
	}
	read := func(r io.Reader, ch chan<- pipeResult) {
		buf := make([]byte, 4096)
		var all []byte
		for {
			n, e := r.Read(buf)
			if n > 0 {
				mark()
				if len(all) < 8192 {
					all = append(all, buf[:n]...)
				}
			}
			if e != nil {
				if e == io.EOF {
					e = nil
				}
				ch <- pipeResult{all, e}
				return
			}
		}
	}
	outCh, errCh := make(chan pipeResult, 1), make(chan pipeResult, 1)
	go read(stdout, outCh)
	go read(stderr, errCh)
	doneRSS := make(chan struct{})
	peakCh := make(chan float64, 1)
	go func(pid int) {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		peak := 0.0
		for {
			if got := processRSSMB(pid); got > peak {
				peak = got
			}
			select {
			case <-doneRSS:
				peakCh <- peak
				return
			case <-ticker.C:
			}
		}
	}(cmd.Process.Pid)
	outResult, errResult := <-outCh, <-errCh
	waitErr := cmd.Wait()
	finished := time.Now()
	close(doneRSS)
	peak := <-peakCh
	if outResult.err != nil {
		return sample{}, outResult.err
	}
	if errResult.err != nil {
		return sample{}, errResult.err
	}
	if firstAt.IsZero() {
		firstAt = finished
	}
	if waitErr != nil {
		return sample{}, fmt.Errorf("%s: %w\n%s", FormatCommand(argv), waitErr, tail(string(append(outResult.data, errResult.data...)), 2048))
	}
	return sample{total: ms(finished.Sub(started)), first: ms(firstAt.Sub(started)), drain: ms(finished.Sub(firstAt)), peak: round(peak)}, nil
}

func Summarize(values []float64) Stats {
	if len(values) == 0 {
		panic("empty performance sample")
	}
	values = append([]float64(nil), values...)
	for i := range values {
		values[i] = round(values[i])
	}
	sort.Float64s(values)
	total := 0.0
	for _, v := range values {
		total += v
	}
	mid := len(values) / 2
	median := values[mid]
	if len(values)%2 == 0 {
		median = round((values[mid-1] + values[mid]) / 2)
	}
	p95 := int(math.Ceil(.95*float64(len(values)))) - 1
	if p95 < 0 {
		p95 = 0
	}
	return Stats{Samples: values, Min: values[0], Median: median, Average: round(total / float64(len(values))), P95: values[p95], Max: values[len(values)-1]}
}

func warnings(m Metrics, t Thresholds) []Warning {
	var out []Warning
	add := func(metric, stat string, got, limit float64, unit string) {
		if limit > 0 && got > limit {
			out = append(out, Warning{Metric: metric, Statistic: stat, Observed: got, Threshold: limit, Unit: unit, Message: fmt.Sprintf("%s %s = %.2f %s exceeds %.2f %s", metric, stat, got, unit, limit, unit)})
		}
	}
	add("coldStartMs", "p95", m.ColdStartMs.P95, t.ColdStartP95Ms, "ms")
	add("firstOutputMs", "p95", m.FirstOutputMs.P95, t.FirstOutputP95Ms, "ms")
	add("peakRssMb", "max", m.PeakRSSMB.Max, t.PeakRSSMaxMB, "MB")
	return out
}

func FormatSummary(r Report) string {
	lines := []string{fmt.Sprintf("SuperCli performance smoke (%s/%s)", r.OS, r.Arch), "command: " + FormatCommand(r.Command), fmt.Sprintf("samples: %d measured, %d warmup", r.Iterations, r.Warmup), fmt.Sprintf("cold start: median %.2f ms, p95 %.2f ms", r.Metrics.ColdStartMs.Median, r.Metrics.ColdStartMs.P95), fmt.Sprintf("first output: median %.2f ms, p95 %.2f ms", r.Metrics.FirstOutputMs.Median, r.Metrics.FirstOutputMs.P95), fmt.Sprintf("process drain: median %.2f ms", r.Metrics.ProcessDrainMs.Median), fmt.Sprintf("peak RSS: max %.2f MB", r.Metrics.PeakRSSMB.Max)}
	if len(r.Warnings) == 0 {
		lines = append(lines, "warnings: none")
	} else {
		lines = append(lines, "warnings:")
		for _, w := range r.Warnings {
			lines = append(lines, "- "+w.Message)
		}
	}
	return strings.Join(lines, "\n")
}

func WriteJSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
func FormatCommand(argv []string) string {
	out := make([]string, len(argv))
	for i, arg := range argv {
		if strings.ContainsAny(arg, " \t\r\n") {
			out[i] = strconv.Quote(arg)
		} else {
			out[i] = arg
		}
	}
	return strings.Join(out, " ")
}
func ms(d time.Duration) float64 { return round(float64(d.Microseconds()) / 1000) }
func round(v float64) float64    { return math.Round(v*100) / 100 }
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
