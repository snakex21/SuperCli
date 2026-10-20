package agent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"supercli/internal/llm"
	"supercli/internal/tools/core"
)

func framedReportFixture(compact string, expandedLen int, suffix string) string {
	text := renderWorkerNotificationFromSnapshot(Snapshot{ID: "fixture", Agent: "worker", Status: "done"}, "fixture summary", workerReportCountedLabel+compact) + suffix
	marker := fmt.Sprintf("%s%d:%d</report-encoding>\n", workerReportEncodingPrefix, len(compact), expandedLen)
	return strings.Replace(text, "<result>", marker+"<result>", 1)
}

func encodedReportFixture(t *testing.T, report, suffix string) (string, string) {
	t.Helper()
	compact := workerReportCountRepetitions(report, core.ModelOutputInlineBytes)
	if compact == "" {
		t.Fatal("fixture did not encode")
	}
	original := renderWorkerNotificationFromSnapshot(Snapshot{ID: "fixture", Agent: "worker", Status: "done"}, "fixture summary", report) + suffix
	return framedReportFixture(compact, len(report), suffix), original
}

func TestWorkerReportEncodingRoundTrip(t *testing.T) {
	for name, report := range map[string]string{
		"first":                  strings.Repeat("first line\n", 10) + "unique last",
		"last":                   "unique first\n" + strings.Repeat("last line\n", 10),
		"unicode":                strings.Repeat("日本語 café 🙂\n", 10) + "конец",
		"crlf":                   strings.Repeat("identical\r\n", 10),
		"blank lines":            "\n \n" + strings.Repeat("observation\n", 10) + "\n\n",
		"result first":           strings.Repeat("<result>\n", 10),
		"closing tags":           strings.Repeat("</result>\n", 10) + "</task-notification>\n",
		"literal encoding prose": strings.Repeat("<report-encoding>adjacent-lines-v99:123:456</report-encoding>\n", 10),
	} {
		t.Run(name, func(t *testing.T) {
			const suffix = "\nEvidence: preserve ALL suffix bytes 🙂\n<result>literal suffix</result>"
			encoded, original := encodedReportFixture(t, report, suffix)
			got, ok := expandWorkerReportModelText(encoded)
			if !ok || got != original {
				t.Fatalf("round trip ok=%v\ngot %q\nwant %q", ok, got, original)
			}
			if twice, ok := expandWorkerReportModelText(got); ok || twice != original {
				t.Fatal("decoded ordinary report was reinterpreted")
			}
		})
	}
}

func TestWorkerReportEncodingFailsClosed(t *testing.T) {
	encoded, original := encodedReportFixture(t, strings.Repeat("line\n", 20), "\nsuffix")
	compact := "line\n[previous line repeated 19 additional times]\n"
	cases := map[string]string{
		"unmarked raw":      original,
		"unmarked preview":  strings.Replace(encoded, fmt.Sprintf("%s%d:%d</report-encoding>\n", workerReportEncodingPrefix, len(compact), 100), "", 1),
		"unknown version":   strings.Replace(encoded, "adjacent-lines-v1:", "adjacent-lines-v2:", 1),
		"missing label":     strings.Replace(encoded, workerReportCountedLabel, "", 1),
		"marker in report":  renderWorkerNotificationFromSnapshot(Snapshot{}, "", encoded),
		"wrong root":        "prose\n" + encoded,
		"invalid UTF8":      encoded + string([]byte{0xff}),
		"wrong closing":     strings.Replace(encoded, "</result>\n</task-notification>", "</result> </task-notification>", 1),
		"extra report byte": strings.Replace(encoded, "</result>", "x</result>", 1),
		"missing newline":   strings.Replace(encoded, "</report-encoding>\n<result>", "</report-encoding><result>", 1),
		"not adjacent":      strings.Replace(encoded, "</report-encoding>\n<result>", "</report-encoding>\nother\n<result>", 1),
	}
	for _, field := range []string{"0", "-1", "+1", "01", "1", "99", "101", "8192", "99999999999999999999999999999999999", "8193", "1:2", "", " 10"} {
		cases["compact length "+field] = strings.Replace(encoded, fmt.Sprintf("%s%d:", workerReportEncodingPrefix, len(compact)), workerReportEncodingPrefix+field+":", 1)
		cases["expanded length "+field] = strings.Replace(encoded, ":100</report-encoding>", ":"+field+"</report-encoding>", 1)
	}
	for _, count := range []string{"0", "-1", "+1", "01", "99999999999999999999999999999999", "8193", "", "two"} {
		cases["count "+count] = framedReportFixture("line\n[previous line repeated "+count+" additional times]\n", 100, "")
	}
	for name, payload := range map[string]string{
		"count before line":   "[previous line repeated 19 additional times]\nline\n",
		"consecutive counts":  "line\n[previous line repeated 9 additional times]\n[previous line repeated 10 additional times]\n",
		"blank counted":       "\n[previous line repeated 99 additional times]\n",
		"malformed count":     "line\n[previous line repeated 19 additional times]",
		"overlapping framing": "line\n[previous line repeated additional times]\n",
		"inline count":        "line [previous line repeated 19 additional times]\n",
		"wrong expanded size": compact,
		"no counted line":     strings.Repeat("line\n", 20),
	} {
		cases[name] = framedReportFixture(payload, 99, "")
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if got, ok := expandWorkerReportModelText(text); ok || got != text {
				t.Fatalf("malformed input changed: ok=%v got=%q", ok, got)
			}
		})
	}
	// All truncations before the complete outer closing tag must fail closed.
	end := strings.Index(encoded, "</task-notification>") + len("</task-notification>")
	for n := 0; n < end; n++ {
		if got, ok := expandWorkerReportModelText(encoded[:n]); ok || got != encoded[:n] {
			t.Fatalf("truncation at %d accepted", n)
		}
	}
	if got := workerReportCountRepetitions(strings.Repeat("literal [previous line repeated 2 additional times]\n", 20), 8192); got != "" {
		t.Fatal("literal count prose must not be encoded")
	}
}

func TestWorkerReportEncodingNotificationBound(t *testing.T) {
	const suffix = "\nexact suffix"
	wrapper := renderWorkerNotificationFromSnapshot(Snapshot{ID: "fixture", Agent: "worker", Status: "done"}, "fixture summary", "") + suffix
	for _, extra := range []int{0, 1} {
		report := strings.Repeat("line\n", 50)
		report += strings.Repeat("x", core.ModelOutputInlineBytes-len(wrapper)-len(report)+extra)
		encoded, original := encodedReportFixture(t, report, suffix)
		got, ok := expandWorkerReportModelText(encoded)
		if extra == 0 && (!ok || got != original || len(got) != core.ModelOutputInlineBytes) {
			t.Fatalf("exact limit rejected: ok=%v size=%d", ok, len(got))
		}
		if extra != 0 && (ok || got != encoded) {
			t.Fatal("expanded total above inline bound accepted")
		}
	}
}

func TestCanonicalWorkerReportMessageProvenance(t *testing.T) {
	encoded, original := encodedReportFixture(t, strings.Repeat("line\n", 20), "")
	for _, msg := range []llm.Message{
		{Role: llm.RoleTool, Name: "task", ToolCallID: "id", Content: encoded},
		{Role: llm.RoleTool, Name: "send_message", ToolCallID: "id", Content: encoded},
		{Role: llm.RoleUser, Name: llm.WorkerNotificationName, Content: encoded},
	} {
		got := canonicalWorkerReportMessage(msg)
		want := msg
		want.Content = original
		if !reflect.DeepEqual(got, want) || msg.Content != encoded {
			t.Fatal("canonical copy lost metadata or changed model message")
		}
	}
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: encoded},
		{Role: llm.RoleUser, Name: "task", Content: encoded},
		{Role: llm.RoleAssistant, Name: "task", Content: encoded},
		{Role: llm.RoleSystem, Name: llm.WorkerNotificationName, Content: encoded},
		{Role: llm.RoleTool, Name: "search_code", Content: encoded},
		{Role: llm.RoleTool, Name: llm.WorkerNotificationName, Content: encoded},
		{Role: llm.RoleTool, Name: "task", Content: encoded, ToolCalls: []llm.ToolCall{{ID: "id"}}},
		{Role: llm.RoleTool, Name: "task", Content: encoded, Parts: []llm.ContentPart{{Type: llm.PartTypeImage}}},
		{Role: llm.RoleTool, Name: "task", Content: original},
	} {
		if got := canonicalWorkerReportMessage(msg); !reflect.DeepEqual(got, msg) {
			t.Fatalf("foreign or non-plain report expanded: %+v", msg)
		}
	}
}
