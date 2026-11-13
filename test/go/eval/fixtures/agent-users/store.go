package users

import (
	"fmt"
	"strings"
)

// Store keeps users in memory in insertion order.
type Store struct {
	users []User
}

// Add stores a new user and returns it with its assigned ID.
func (s *Store) Add(name string) User {
	u := User{ID: len(s.users) + 1, Name: name}
	s.users = append(s.users, u)
	return u
}

// ExportCSV renders all users as CSV with a header row.
func (s *Store) ExportCSV() string {
	var b strings.Builder
	b.WriteString("id,name\n")
	for _, u := range s.users {
		fmt.Fprintf(&b, "%d,%s\n", u.ID, u.Name)
	}
	return b.String()
}
