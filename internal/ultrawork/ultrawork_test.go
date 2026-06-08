package ultrawork

import (
	"context"
	"strings"
	"testing"
)

// --- Detect ---------------------------------------------------------------

func TestDetect_Empty(t *testing.T) {
	if Detect("") {
		t.Fatal("Detect(\"\") = true, want false")
	}
}

func TestDetect_UltraworkKeyword(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Bare keyword
		{"ultrawork", true},
		{"  ultrawork  ", true},
		{"ULTRawork", true},
		{"UltraWork", true},
		// Keyword embedded in a sentence
		{"ultrawork the migration from postgres to mysql", true},
		{"please run this in ULTRAWORK mode", true},
		// The "ulw" abbreviation
		{"ulw migrate to v2", true},
		{"ULW migrate to v2", true},
		// "ulw" as part of a longer word — must NOT match
		{"elaborate walkthrough", false},      // no "ulw" substring at all
		{"buluwbuluw", false},                 // "ulw" is embedded in "buluw" — not standalone
		{"gulwarble", false},                  // "ulw" is part of a longer word
		// No keyword
		{"please refactor the parser", false},
		{"", false},
		// Edge cases
		{"ulw.", true},                        // followed by punctuation
		{"(ulw)", true},                       // surrounded by punctuation
		{" ulw ,", true},                      // space + comma
		{"xulw", false},                       // "ulw" preceded by a letter
		{"ulwx", false},                       // "ulw" followed by a letter
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := Detect(c.in); got != c.want {
				t.Errorf("Detect(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestDetect_UltraworkSubstringTakesPrecedence(t *testing.T) {
	// "ultrawork" inside a longer word should still match
	// (it's a unique enough substring).
	if !Detect("anultraworkinjected") {
		t.Error("Detect should match 'ultrawork' as a substring even without word boundaries")
	}
}

// --- containsWord ---------------------------------------------------------

func TestContainsWord(t *testing.T) {
	// containsWord expects pre-lowercased input (the caller
	// in Detect() does strings.ToLower first). All test
	// inputs are lowercase as a result.
	cases := []struct {
		haystack string
		needle   string
		want     bool
	}{
		{"ulw migrate", "ulw", true},
		{"ulw migrate", "ulw", true},
		{"ulw", "ulw", true},
		{"", "ulw", false},
		{"ulwx", "ulw", false},
		{"xulw", "ulw", false},
		{"buluw", "ulw", false},
		{"ulw.", "ulw", true},
		{"(ulw)", "ulw", true},
		{"", "", false},
	}
	for _, c := range cases {
		got := containsWord(c.haystack, c.needle)
		if got != c.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v", c.haystack, c.needle, got, c.want)
		}
	}
}

func TestIsLetter(t *testing.T) {
	if !isLetter('a') || !isLetter('Z') {
		t.Error("isLetter should be true for ASCII letters")
	}
	if isLetter('0') || isLetter(' ') || isLetter('_') {
		t.Error("isLetter should be false for digits, spaces, underscores")
	}
}

// --- SystemPromptSection --------------------------------------------------

func TestSystemPromptSection_NotEmpty(t *testing.T) {
	s := SystemPromptSection()
	if s == "" {
		t.Fatal("SystemPromptSection() = \"\", want non-empty")
	}
	if !strings.Contains(s, "ULTRAWORK MODE ACTIVE") {
		t.Errorf("SystemPromptSection missing 'ULTRAWORK MODE ACTIVE' marker; got:\n%s", s)
	}
	if !strings.Contains(s, "Sisyphus") {
		t.Error("SystemPromptSection should mention Sisyphus so the model knows what to expect")
	}
	if !strings.Contains(s, "/goal") {
		t.Error("SystemPromptSection should mention /goal so the model knows the source of truth")
	}
}

// --- Gate mocks -----------------------------------------------------------

type fakeGoal struct {
	id           string
	title        string
	unfinished   int
	callsToCount int
}

func (f *fakeGoal) ActiveID() string                  { return f.id }
func (f *fakeGoal) ActiveTitle() string               { return f.title }
func (f *fakeGoal) UnfinishedTasks(_ context.Context) int {
	f.callsToCount++
	return f.unfinished
}

type fakeCredit struct {
	hasBudget  bool
	remSess    int64
	remDay     int64
	remCalls   int
}

func (f *fakeCredit) Remaining(_ context.Context) (int64, int64) {
	f.remCalls++
	return f.remSess, f.remDay
}
func (f *fakeCredit) HasBudget() bool { return f.hasBudget }

// --- CheckGates -----------------------------------------------------------

func TestCheckGates_NilGoal(t *testing.T) {
	res := CheckGates(nil, nil)
	if res.OK {
		t.Fatal("CheckGates(nil, nil) OK = true, want false (no active /goal)")
	}
	if !strings.Contains(res.Reason, "no /goal active") {
		t.Errorf("reason should mention /goal; got %q", res.Reason)
	}
}

func TestCheckGates_GoalWithEmptyID(t *testing.T) {
	res := CheckGates(&fakeGoal{}, nil)
	if res.OK {
		t.Fatal("CheckGates with empty ActiveID = true, want false")
	}
}

func TestCheckGates_GoalWithTitleButNoID(t *testing.T) {
	// Defensive: if a future adapter exposes a title but the
	// id is empty, the message should still help the user.
	res := CheckGates(&fakeGoal{title: "migrate db"}, nil)
	if res.OK {
		t.Fatal("OK = true, want false")
	}
	if !strings.Contains(res.Reason, "migrate db") {
		t.Errorf("reason should mention the title for diagnosis; got %q", res.Reason)
	}
}

func TestCheckGates_GoalSetCreditNil(t *testing.T) {
	res := CheckGates(&fakeGoal{id: "g1", title: "goal"}, nil)
	if !res.OK {
		t.Fatalf("OK = false, want true (F7 off = no credit cap); reason=%q", res.Reason)
	}
	if !strings.Contains(res.Reason, "F7 off") {
		t.Errorf("reason should mention F7 off; got %q", res.Reason)
	}
}

func TestCheckGates_GoalSetCreditNoBudget(t *testing.T) {
	res := CheckGates(
		&fakeGoal{id: "g1", title: "goal"},
		&fakeCredit{hasBudget: false},
	)
	if !res.OK {
		t.Fatalf("OK = false, want true (HasBudget=false); reason=%q", res.Reason)
	}
}

func TestCheckGates_CreditHasBudgetAndRemaining(t *testing.T) {
	res := CheckGates(
		&fakeGoal{id: "g1"},
		&fakeCredit{hasBudget: true, remSess: 5000, remDay: 50000},
	)
	if !res.OK {
		t.Fatalf("OK = false, want true; reason=%q", res.Reason)
	}
	if !strings.Contains(res.Reason, "5000") || !strings.Contains(res.Reason, "50000") {
		t.Errorf("reason should report remaining tokens; got %q", res.Reason)
	}
}

func TestCheckGates_CreditExhausted(t *testing.T) {
	res := CheckGates(
		&fakeGoal{id: "g1"},
		&fakeCredit{hasBudget: true, remSess: 0, remDay: 0},
	)
	if res.OK {
		t.Fatal("OK = true, want false (no credits remaining)")
	}
	if !strings.Contains(res.Reason, "out of credits") {
		t.Errorf("reason should explain credit exhaustion; got %q", res.Reason)
	}
}

// --- Sisyphus -------------------------------------------------------------

func TestSisyphus_NilGoalNoOp(t *testing.T) {
	s := &Sisyphus{}
	if cont, msg := s.ShouldContinue(context.Background()); cont || msg != "" {
		t.Errorf("Sisyphus with nil Goal should be no-op; got cont=%v msg=%q", cont, msg)
	}
}

func TestSisyphus_NilReceiverNoOp(t *testing.T) {
	var s *Sisyphus
	if cont, msg := s.ShouldContinue(context.Background()); cont || msg != "" {
		t.Errorf("nil Sisyphus should be no-op; got cont=%v msg=%q", cont, msg)
	}
}

func TestSisyphus_NoActiveGoal(t *testing.T) {
	s := &Sisyphus{Goal: &fakeGoal{}}
	if cont, _ := s.ShouldContinue(context.Background()); cont {
		t.Error("Sisyphus should not enforce when there is no active goal")
	}
}

func TestSisyphus_AllTasksDone(t *testing.T) {
	s := &Sisyphus{Goal: &fakeGoal{id: "g1", unfinished: 0}}
	if cont, _ := s.ShouldContinue(context.Background()); cont {
		t.Error("Sisyphus should not enforce when all tasks are done")
	}
}

func TestSisyphus_UnfinishedTasksReminder(t *testing.T) {
	g := &fakeGoal{id: "g1", title: "migrate db", unfinished: 3}
	s := &Sisyphus{Goal: g}
	cont, msg := s.ShouldContinue(context.Background())
	if !cont {
		t.Fatal("Sisyphus should re-prompt when tasks remain")
	}
	if !strings.Contains(msg, "3 todo") {
		t.Errorf("reminder should mention 3 todos; got %q", msg)
	}
	if !strings.Contains(msg, "Sisyphus @1/3") {
		t.Errorf("reminder should label the attempt counter; got %q", msg)
	}
}

func TestSisyphus_CapAfterMaxConsecutive(t *testing.T) {
	g := &fakeGoal{id: "g1", unfinished: 5}
	s := &Sisyphus{Goal: g, MaxConsecutive: 3}
	for i := 1; i <= 3; i++ {
		cont, _ := s.ShouldContinue(context.Background())
		if !cont {
			t.Fatalf("Sisyphus should continue on attempt %d (under cap)", i)
		}
	}
	// 4th call: cap hit
	cont, _ := s.ShouldContinue(context.Background())
	if cont {
		t.Error("Sisyphus should stop enforcing once MaxConsecutive is exceeded")
	}
}

func TestSisyphus_ResetClearsCounter(t *testing.T) {
	g := &fakeGoal{id: "g1", unfinished: 2}
	s := &Sisyphus{Goal: g, MaxConsecutive: 1}
	if cont, _ := s.ShouldContinue(context.Background()); !cont {
		t.Fatal("first call should continue")
	}
	if cont, _ := s.ShouldContinue(context.Background()); cont {
		t.Fatal("second call should hit cap and stop")
	}
	s.Reset()
	if cont, _ := s.ShouldContinue(context.Background()); !cont {
		t.Error("after Reset, Sisyphus should continue again")
	}
}

func TestSisyphus_ZeroMaxConsecutiveDefaultsTo3(t *testing.T) {
	g := &fakeGoal{id: "g1", unfinished: 1}
	s := &Sisyphus{Goal: g} // MaxConsecutive = 0 → default 3
	for i := 1; i <= 3; i++ {
		cont, _ := s.ShouldContinue(context.Background())
		if !cont {
			t.Fatalf("default cap should allow attempt %d; got cont=false", i)
		}
	}
	if cont, _ := s.ShouldContinue(context.Background()); cont {
		t.Error("4th attempt should be over the default cap of 3")
	}
}

func TestSisyphus_TasksZeroAfterPriorEnforcementResets(t *testing.T) {
	// Simulate: model was being re-prompted, then the user
	// marked the remaining tasks done/skipped. Sisyphus
	// should observe the zero and reset the counter so the
	// NEXT run starts clean.
	g := &fakeGoal{id: "g1", unfinished: 2}
	s := &Sisyphus{Goal: g, MaxConsecutive: 3}
	if cont, _ := s.ShouldContinue(context.Background()); !cont {
		t.Fatal("first call should continue")
	}
	// User marked them all done
	g.unfinished = 0
	cont, _ := s.ShouldContinue(context.Background())
	if cont {
		t.Error("with 0 unfinished tasks, Sisyphus should stop")
	}
	// And a new round of unfinished work should be allowed
	g.unfinished = 1
	if cont, _ := s.ShouldContinue(context.Background()); !cont {
		t.Error("after zero resets the counter, a new round of tasks should re-trigger Sisyphus")
	}
}

// --- Mode -----------------------------------------------------------------

func TestMode_String(t *testing.T) {
	if got := ModeOff.String(); got != "off" {
		t.Errorf("ModeOff.String() = %q, want \"off\"", got)
	}
	if got := ModeOn.String(); got != "on" {
		t.Errorf("ModeOn.String() = %q, want \"on\"", got)
	}
}
