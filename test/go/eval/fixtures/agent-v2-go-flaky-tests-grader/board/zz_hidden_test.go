package board

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"
)

type slowSource struct {
	e     Entry
	delay time.Duration
	err   error
}

func (s slowSource) Fetch(context.Context) (Entry, error) {
	time.Sleep(s.delay)
	runtime.Gosched()
	return s.e, s.err
}

func TestHiddenCollectOrderAndFirstError(t *testing.T) {
	for round := 0; round < 30; round++ {
		var srcs []Source
		for i := 0; i < 6; i++ {
			srcs = append(srcs, slowSource{e: Entry{Name: fmt.Sprint(i), Score: i}, delay: time.Duration(6-i) * 200 * time.Microsecond})
		}
		got, err := CollectAll(context.Background(), srcs)
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range got {
			if e.Score != i {
				t.Fatalf("order = %v", got)
			}
		}
		early, late := errors.New("early"), errors.New("late")
		srcs = []Source{
			slowSource{delay: 0},
			slowSource{err: early, delay: 2 * time.Millisecond},
			slowSource{err: late, delay: 0},
		}
		if _, err := CollectAll(context.Background(), srcs); err != early {
			t.Fatalf("error = %v, want the earliest failing source's error", err)
		}
	}
}

func TestHiddenRankTiesAndTop(t *testing.T) {
	for round := 0; round < 50; round++ {
		got := Top(map[string]int{"z": 3, "y": 3, "x": 3, "w": 9, "v": 3, "u": 3, "t": 3, "s": 3, "r": 3}, 4)
		if fmt.Sprint(got) != "[{w 9} {r 3} {s 3} {t 3}]" {
			t.Fatalf("Top = %v", got)
		}
	}
}
