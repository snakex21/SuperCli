// Package routes maps paths to backends.
package routes

import "gateway/internal/util"

// Route is one mapping.
type Route struct {
	ID      string
	Path    string
	Backend string
}

// New builds a route with a generated id and a slug path.
func New(title, backend string) Route {
	return Route{ID: util.NewID("rt"), Path: "/" + util.Slugify(title), Backend: backend}
}
