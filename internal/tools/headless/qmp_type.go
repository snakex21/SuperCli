package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"supercli/internal/tools/core"
)

// US-layout qcodes. Shifted characters reuse the base key with shift held.
var qmpPlainKeys = map[rune]string{' ': "spc", '\n': "ret", '\t': "tab", '-': "minus", '=': "equal",
	'[': "bracket_left", ']': "bracket_right", '\\': "backslash", ';': "semicolon", '\'': "apostrophe",
	'`': "grave_accent", ',': "comma", '.': "dot", '/': "slash"}
var qmpShiftedKeys = map[rune]string{'!': "1", '@': "2", '#': "3", '$': "4", '%': "5", '^': "6", '&': "7",
	'*': "8", '(': "9", ')': "0", '_': "minus", '+': "equal", '{': "bracket_left", '}': "bracket_right",
	'|': "backslash", ':': "semicolon", '"': "apostrophe", '~': "grave_accent", '<': "comma", '>': "dot", '?': "slash"}

// Polish programmer layout: AltGr + base letter, Shift for capitals.
var qmpPolishKeys = map[rune]string{'ą': "a", 'ć': "c", 'ę': "e", 'ł': "l", 'ń': "n", 'ó': "o", 'ś': "s", 'ź': "x", 'ż': "z"}

const (
	qmpKeyHold        = 20 * time.Millisecond
	defaultQMPTypeGap = 40 * time.Millisecond
	defaultQMPSettle  = 300 * time.Millisecond
	maxQMPTypeGapMS   = 1000
	maxQMPSettleMS    = 10000
)

type qmpStroke struct {
	key   string
	shift bool
	altgr bool
}

// qmpTypeStrokes maps the whole text before anything is sent, so an
// unsupported character never leaves a half-typed command in the guest.
func qmpTypeStrokes(text, layout string) ([]qmpStroke, error) {
	if layout != "" && layout != "us" && layout != "pl" {
		return nil, fmt.Errorf("QMP layout must be us or pl")
	}
	strokes := make([]qmpStroke, 0, len(text))
	for i, r := range text {
		lower := []rune(strings.ToLower(string(r)))[0]
		switch {
		case r == '\r':
			continue
		case layout == "pl" && qmpPolishKeys[lower] != "":
			strokes = append(strokes, qmpStroke{key: qmpPolishKeys[lower], shift: lower != r, altgr: true})
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			strokes = append(strokes, qmpStroke{key: string(r)})
		case r >= 'A' && r <= 'Z':
			strokes = append(strokes, qmpStroke{key: string(r + 'a' - 'A'), shift: true})
		default:
			if key, ok := qmpPlainKeys[r]; ok {
				strokes = append(strokes, qmpStroke{key: key})
			} else if key, ok := qmpShiftedKeys[r]; ok {
				strokes = append(strokes, qmpStroke{key: key, shift: true})
			} else {
				return nil, fmt.Errorf("QMP type: unsupported character %q at byte %d (ASCII, plus Polish letters with layout=pl; use keys for special keys)", r, i)
			}
		}
	}
	if len(strokes) == 0 {
		return nil, fmt.Errorf("QMP type requires text")
	}
	return strokes, nil
}

func qmpTypeGap(p params) time.Duration {
	if p.DelayMS > 0 {
		return time.Duration(p.DelayMS) * time.Millisecond
	}
	return defaultQMPTypeGap
}

func qmpSettle(p params) time.Duration {
	if p.SettleMS > 0 {
		return time.Duration(p.SettleMS) * time.Millisecond
	}
	return defaultQMPSettle
}

func validateQMPType(p params) error {
	if p.DelayMS < 0 || p.DelayMS > maxQMPTypeGapMS {
		return fmt.Errorf("QMP delay_ms must be 0..%d", maxQMPTypeGapMS)
	}
	strokes, err := qmpTypeStrokes(p.Text, p.Layout)
	if err != nil {
		return err
	}
	estimate := time.Duration(len(strokes)) * (qmpKeyHold + qmpTypeGap(p))
	if p.ScreenshotAfter {
		estimate += qmpSettle(p)
	}
	if limit := time.Duration(p.TimeoutMS) * time.Millisecond; estimate >= limit {
		return fmt.Errorf("QMP type needs about %s for %d characters; raise timeout_ms or split the text", estimate.Round(time.Second), len(strokes))
	}
	return nil
}

func qmpKeyEvent(key string, down bool) map[string]any {
	return map[string]any{"type": "key", "data": map[string]any{"down": down, "key": map[string]string{"type": "qcode", "data": key}}}
}

// typeText presses and releases each key separately with a short hold, as
// fast batched events can be dropped by slow guests (PS/2, early boot).
func (q *qmpClient) typeText(ctx context.Context, strokes []qmpStroke, gap time.Duration) error {
	for i, s := range strokes {
		down, up := []any{}, []any{}
		if s.altgr {
			down = append(down, qmpKeyEvent("alt_r", true))
		}
		if s.shift {
			down = append(down, qmpKeyEvent("shift", true))
		}
		down = append(down, qmpKeyEvent(s.key, true))
		up = append(up, qmpKeyEvent(s.key, false))
		if s.shift {
			up = append(up, qmpKeyEvent("shift", false))
		}
		if s.altgr {
			up = append(up, qmpKeyEvent("alt_r", false))
		}
		err := sendQMPEvents(ctx, q, down)
		if err == nil {
			err = sleepCtx(ctx, qmpKeyHold)
		}
		if err == nil {
			err = sendQMPEvents(ctx, q, up)
		}
		if err == nil && i < len(strokes)-1 {
			err = sleepCtx(ctx, gap)
		}
		if err != nil {
			return fmt.Errorf("QMP type stopped after %d of %d characters: %w", i, len(strokes), err)
		}
	}
	return nil
}

func sendQMPEvents(ctx context.Context, q *qmpClient, events []any) error {
	_, err := q.command(ctx, "input-send-event", map[string]any{"events": events})
	return err
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// screenshotAfter captures the result of an input action on the same QMP
// connection, saving the model a separate request just to look.
func (t *Tool) screenshotAfter(ctx context.Context, q *qmpClient, p params) (core.Result, error) {
	if err := sleepCtx(ctx, qmpSettle(p)); err != nil {
		return core.Result{}, err
	}
	result, err := t.qmpScreenshot(ctx, q, p)
	if err != nil {
		return core.Result{}, fmt.Errorf("%s done, but screenshot failed: %w", p.Action, err)
	}
	var fields map[string]any
	if json.Unmarshal([]byte(result.Text), &fields) == nil {
		fields["action"], fields["ok"] = p.Action, true
		if data, err := json.Marshal(fields); err == nil {
			result.Text = string(data)
		}
	}
	return result, nil
}
