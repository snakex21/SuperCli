package pager

import "errors"

// MaxPageSize caps the number of items returned by one List call.
const MaxPageSize = 100

// ErrBadLimit is returned (possibly wrapped) when limit is not positive.
var ErrBadLimit = errors.New("pager: limit must be positive")

// Page is one page of results. Next is the cursor for the following page,
// or "" when there are no more items.
type Page struct {
	Items []Item
	Next  string
}

// List returns up to limit items (capped at MaxPageSize) in ascending ID order,
// starting after the position encoded in cursor ("" = from the beginning).
func (s *Store) List(cursor string, limit int) (Page, error) {
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	start := 0
	if cursor != "" {
		id, err := decodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		start = s.firstAfter(id)
	}
	end := start + limit
	if end > len(s.items) {
		end = len(s.items)
	}
	items := s.items[start:end]
	page := Page{Items: items}
	if len(items) == limit {
		page.Next = encodeCursor(items[len(items)-1].ID)
	}
	return page, nil
}
