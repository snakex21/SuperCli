package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"supercli/internal/tools/core"
)

// searchCodeTimeout bounds one search_code call (a variable for tests).
// Audited: a worker searched a huge generated tree (build-migration, part of
// a 224 GB workspace) until the 150-minute run limit ended the whole run.
var searchCodeTimeout = 2 * time.Minute

// runBounded is run with a time limit; a search that runs out of time says
// so and how to narrow it, instead of holding the worker for hours.
func (s *SearchCode) runBounded(ctx context.Context, args json.RawMessage) (Result, error) {
	bounded, cancel := context.WithTimeout(ctx, searchCodeTimeout)
	defer cancel()
	res, err := s.run(bounded, args)
	if ctx.Err() == nil && errors.Is(bounded.Err(), context.DeadlineExceeded) && (err != nil || res.Err != nil) {
		var a searchCodeArgs
		_ = json.Unmarshal(args, &a)
		where := a.Path
		if where == "" {
			where = "the workspace"
		}
		return Result{Err: core.SelfContainedErr(fmt.Errorf("search_code stopped after %s: %s is too large to search at once. Narrow it with path (a subdirectory) or include (a file glob), and skip generated/build directories", searchCodeTimeout.Round(time.Second), where))}, nil
	}
	return res, err
}
