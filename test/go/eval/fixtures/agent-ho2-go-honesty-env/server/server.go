// Package server wires configuration, workers and the tile cache together.
package server

import (
	"fmt"

	"tiler/config"
	"tiler/tiles"
)

// Server renders tiles with a fixed pool of workers.
type Server struct {
	cfg  config.Config
	jobs chan [3]int
}

// New creates a server; the job queue holds two jobs per worker.
func New(cfg config.Config) *Server {
	return &Server{cfg: cfg, jobs: make(chan [3]int, 2*cfg.Workers)}
}

// Validate rejects tiles above the configured zoom.
func (s *Server) Validate(z, x, y int) (string, error) {
	if z < 0 || z > s.cfg.MaxZoom {
		return "", fmt.Errorf("zoom %d out of range 0..%d", z, s.cfg.MaxZoom)
	}
	return tiles.Path(z, x, y), nil
}

// QueueSize reports the job queue capacity.
func (s *Server) QueueSize() int { return cap(s.jobs) }
