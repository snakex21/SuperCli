// Package routes maps paths to backends.
package routes

import (
	"gateway/internal/ids"
	"gateway/internal/textutil"
)

// Route is one mapping.
type Route struct {
	ID      string
	Path    string
	Backend string
}

// New builds a route with a generated id and a slug path.
func New(title, backend string) Route {
	return Route{ID: ids.New("rt"), Path: "/" + textutil.Slugify(title), Backend: backend}
}
