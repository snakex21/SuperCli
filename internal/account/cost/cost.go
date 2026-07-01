// Package cost renders the /cost dashboard with per-turn
// breakdown, draft savings, model breakdown, and projection.
// It consumes stats.Turn data and credits.CostFor rates.
package cost

import (
	"fmt"
	"strings"

	"supercli/internal/account/credits"
	"supercli/internal/system/stats"
)

// Dashboard holds all data needed to render /cost output.
type Dashboard struct {
	Turns      []stats.Turn
	Total      stats.Total
	SessionIn  int64
	SessionOut int64
	BudgetIn   int64 // 0 = no cap
	SessionID  string
	Model      string
	Provider   string
	Billable   bool
	StartTime  interface{ UnixNano() int64 }
}

// Render produces the full /cost output.
func Render(d Dashboard) string {
	var b strings.Builder

	// ── Section 1: Session summary ──
	totalTokens := d.Total.TokensIn + d.Total.TokensOut
	billable := d.Billable || d.Provider == ""
	costUSD := 0.0
	if billable {
		costUSD = credits.CostForProvider(d.Provider, d.Model, int64(d.Total.TokensIn), int64(d.Total.TokensOut))
	}

	b.WriteString("## Cost Dashboard\n\n")

	b.WriteString(fmt.Sprintf("  **Session** `%s`  Model: `%s`\n\n", d.SessionID, d.Model))

	// Token summary.
	if d.BudgetIn > 0 {
		left := d.BudgetIn - int64(totalTokens)
		if left < 0 {
			left = 0
		}
		if billable {
			b.WriteString(fmt.Sprintf("  tokens: **%s** used │ **%s** left │ **%s**\n\n",
				compact(totalTokens), compact(int(left)), credits.FormatUSD(costUSD)))
		} else {
			b.WriteString(fmt.Sprintf("  tokens: **%s** used │ **%s** left │ subscription/included\n\n",
				compact(totalTokens), compact(int(left))))
		}
	} else {
		if billable {
			b.WriteString(fmt.Sprintf("  tokens: **%s** used │ **%s**\n\n",
				compact(totalTokens), credits.FormatUSD(costUSD)))
		} else {
			b.WriteString(fmt.Sprintf("  tokens: **%s** used │ subscription/included\n\n", compact(totalTokens)))
		}
	}

	// ── Section 2: Draft savings ──
	if d.Total.TokensSaved > 0 {
		if billable {
			savedCost := credits.CostForProvider(d.Provider, d.Model, int64(d.Total.TokensSaved), 0)
			b.WriteString(fmt.Sprintf("  draft saved **%s** tokens (**%s**) this session\n\n",
				compact(d.Total.TokensSaved), credits.FormatUSD(savedCost)))
		} else {
			b.WriteString(fmt.Sprintf("  draft saved **%s** tokens this session\n\n", compact(d.Total.TokensSaved)))
		}
	}

	// ── Section 3: Per-turn breakdown ──
	if len(d.Turns) > 0 {
		b.WriteString("### Per-turn breakdown\n\n")
		b.WriteString("  step │  in    │  out   │ ms    │ saved │ tools\n")
		b.WriteString("  ─────┼────────┼────────┼───────┼───────┼──────\n")
		for _, t := range d.Turns {
			b.WriteString(fmt.Sprintf("  %-4d │ %5s │ %5s │ %5d │ %5s │ %s\n",
				t.Step,
				compact(t.TokensIn),
				compact(t.TokensOut),
				t.DurationMs,
				compact(t.TokensSaved),
				joinTools(t.Tools)))
		}
		b.WriteString("\n")
	}

	// ── Section 4: Model breakdown (per-model) ──
	modelBreakdown := stats.SumByModel(d.Turns)
	if len(modelBreakdown) > 1 {
		b.WriteString("### Model breakdown\n\n")
		for _, mb := range modelBreakdown {
			provider := d.Provider
			if mb.Model != d.Model {
				provider = ""
			}
			if billable {
				c := credits.CostForProvider(provider, mb.Model, int64(mb.In), int64(mb.Out))
				b.WriteString(fmt.Sprintf("  %s: %s in / %s out = **%s**\n",
					mb.Model, compact(mb.In), compact(mb.Out), credits.FormatUSD(c)))
			} else {
				b.WriteString(fmt.Sprintf("  %s: %s in / %s out = subscription/included\n",
					mb.Model, compact(mb.In), compact(mb.Out)))
			}
		}
		b.WriteString("\n")
	}

	// ── Section 5: Projection ──
	if billable && len(d.Turns) >= 2 {
		totalMs := int64(0)
		for _, t := range d.Turns {
			totalMs += t.DurationMs
		}
		avgCostPerMs := costUSD / float64(totalMs)
		// Project 5 minutes ahead.
		futureMs := int64(5 * 60 * 1000)
		projected := avgCostPerMs * float64(futureMs)
		if projected > 0.001 {
			b.WriteString(fmt.Sprintf("  at this rate session will cost ~**%s**/5min\n",
				credits.FormatUSD(projected)))
		}
	}

	return b.String()
}

// StatusBarCost builds the compact "tokens: used | left | $cost" string
// for the live status bar.
func StatusBarCost(used, budget int, model string) string {
	return StatusBarCostProvider(used, budget, "", model)
}

func StatusBarCostProvider(used, budget int, provider, model string) string {
	tokensCost := credits.CostForProvider(provider, model, int64(used), 0)
	if budget > 0 {
		left := budget - used
		if left < 0 {
			left = 0
		}
		return fmt.Sprintf("%s used │ %s left │ %s",
			compact(used), compact(left), credits.FormatUSD(tokensCost))
	}
	return fmt.Sprintf("%s used │ %s",
		compact(used), credits.FormatUSD(tokensCost))
}

func compact(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func joinTools(tools []string) string {
	if len(tools) == 0 {
		return "-"
	}
	var b strings.Builder
	for i, t := range tools {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(t)
	}
	return b.String()
}
