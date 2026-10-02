package agent

import (
	"math/rand"
	"strings"
	"testing"
)

// Preserve the previous probe and transformation as a differential reference:
// Unicode lowercase and regex case folding intentionally have distinct rules.
func legacyCaptureThinkingForScanTest(s string) (plain, captured string) {
	low := strings.ToLower(s)
	if !strings.Contains(low, "<think") && !strings.Contains(low, "<reasoning") && !strings.Contains(low, "<reflection") {
		return s, ""
	}
	var kept, taken strings.Builder
	last := 0
	for _, m := range thinkBlockRe.FindAllStringSubmatchIndex(s, -1) {
		kept.WriteString(s[last:m[0]])
		taken.WriteString(s[m[0]:m[1]])
		taken.WriteByte('\n')
		last = m[1]
	}
	kept.WriteString(s[last:])
	plain = kept.String()
	// Unclosed opening tag (truncated stream): capture from the tag on.
	if open := thinkOpenRe.FindStringIndex(plain); open != nil {
		taken.WriteString(plain[open[0]:])
		plain = plain[:open[0]]
	}
	for strings.Contains(plain, "\n\n\n") {
		plain = strings.ReplaceAll(plain, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(plain), strings.TrimSpace(taken.String())
}

func TestReasoningTagPrefixMatchesLegacyProbe(t *testing.T) {
	for _, s := range reasoningScanParityInputs() {
		low := strings.ToLower(s)
		want := strings.Contains(low, "<think") || strings.Contains(low, "<reasoning") || strings.Contains(low, "<reflection")
		if got := hasReasoningTagPrefix(s); got != want {
			t.Fatalf("probe(%q)=%v, legacy=%v", s, got, want)
		}
	}
}

func TestCaptureThinkingScanPreservesBytesAndUnicode(t *testing.T) {
	for _, s := range reasoningScanParityInputs() {
		wantPlain, wantThinking := legacyCaptureThinkingForScanTest(s)
		gotPlain, gotThinking := captureThinking(s)
		if gotPlain != wantPlain || gotThinking != wantThinking {
			t.Fatalf("input=%q got=%q/%q legacy=%q/%q", s, gotPlain, gotThinking, wantPlain, wantThinking)
		}
	}
}

func reasoningScanParityInputs() []string {
	inputs := []string{
		"Mixed CASE plain text", " \tLeading and Trailing \n", "<div>Mixed CODE</div>",
		"<think>secret</think>Final", "<THINKING>secret</THINKING>Final",
		"<thinking>unfinished", " <thinkxxx>Value \n",
		"<reasoning>secret</reasoning>Answer", "<reflection>secret</reflection>Answer",
		"  Préface🙂<THINK>sekret</THINK> końcówka  ",
		" <THİNK>secret</THİNK>Final ", " <REFLECTİON>secret</REFLECTİON>Answer ",
		"<think>secret</reasoning>different closing tag", "<think></think>\n\n\nValue",
		"<THINK><think>nested</think>after", "<thy>value", "<", "<think", "<reflection",
		"<\xffTHINK>invalid bytes", "<t\xffhink>partial malformed marker",
		strings.Repeat("🌍Mixed CODE; ", 400) + "<ThInK>reason</ThInK>final",
		"<" + strings.Repeat("x", 100) + "<THINK>partial",
	}
	rng := rand.New(rand.NewSource(20261002))
	alphabet := []rune("<>/thinkingreasonreflectionTHINKINGREASONREFLECTION abcXYZ\n\tİKſΣŚ🌍")
	for i := 0; i < 3000; i++ {
		runes := make([]rune, rng.Intn(100))
		for j := range runes {
			runes[j] = alphabet[rng.Intn(len(alphabet))]
		}
		s := string(runes)
		if i%4 == 0 {
			marker := []string{"<THINK", "<refLECtion", "<reASoning", "<thİNK"}[(i/4)%4]
			s += marker
		}
		inputs = append(inputs, s)
	}
	return inputs
}

func TestCaptureThinkingUnmarkedReplyDoesNotAllocate(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("Observed VALUE; Mixed CASE CODE.\n", 300),
		strings.Repeat("Świat 🌍; odpowiedź bez tagów.\n", 300),
		strings.Repeat("<div>Observed VALUE</div><table>CODE</table>\n", 300),
	} {
		if allocations := testing.AllocsPerRun(100, func() { thinkingScanPlainSink, thinkingScanReasoningSink = captureThinking(text) }); allocations != 0 {
			t.Fatalf("unmarked reply allocated: %g", allocations)
		}
		if thinkingScanPlainSink != text || thinkingScanReasoningSink != "" {
			t.Fatal("unmarked reply bytes changed")
		}
	}
}

var thinkingScanPlainSink, thinkingScanReasoningSink string

func BenchmarkCaptureThinkingScan(b *testing.B) {
	for _, input := range []struct{ name, text string }{
		{"answer8KiB", strings.Repeat("Observed VALUE; Mixed CASE Results with CODE; preserve finding.\n", 140)},
		{"answer256KiB", strings.Repeat("Observed VALUE; Mixed CASE Results with CODE; preserve finding.\n", 4500)},
		{"html8KiB", strings.Repeat("<div>Observed VALUE</div><table>CODE</table>\n", 220)},
		{"realThinking", "<THINKING>" + strings.Repeat("Private Reasoning.\n", 300) + "</THINKING>Visible Final"},
	} {
		for _, variant := range []string{"legacy", "scan"} {
			b.Run(input.name+"/"+variant, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(input.text)))
				for b.Loop() {
					if variant == "legacy" {
						thinkingScanPlainSink, thinkingScanReasoningSink = legacyCaptureThinkingForScanTest(input.text)
					} else {
						thinkingScanPlainSink, thinkingScanReasoningSink = captureThinking(input.text)
					}
				}
			})
		}
	}
}
