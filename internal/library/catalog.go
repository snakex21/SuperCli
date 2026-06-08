package library

// defaultCatalog returns the built-in library alternatives
// mapping. Each entry is curated — no mass-scraping, no
// LLM hallucinations. Sources: bundlephobia, npm trends,
// GitHub stars, community consensus (2024-2026).
func defaultCatalog() []Entry {
	return []Entry{
		// JavaScript / TypeScript
		{Library: "moment.js", Task: "date formatting", Alternative: "dayjs", Reason: "2 KB vs 70 KB, same API, plugin system", Confidence: 0.95},
		{Library: "moment", Task: "date formatting", Alternative: "dayjs", Reason: "2 KB vs 70 KB, same API, plugin system", Confidence: 0.95},
		{Library: "moment.js", Task: "tree-shaking", Alternative: "date-fns", Reason: "modular — import only what you use, zero deps", Confidence: 0.9},
		{Library: "lodash", Task: "tree-shaking", Alternative: "lodash-es", Reason: "ESM build enables tree-shaking; same API", Confidence: 0.85},
		{Library: "lodash", Task: "bundle size", Alternative: "radash", Reason: "zero deps, fully typed, 5 KB gzipped", Confidence: 0.8},
		{Library: "axios", Task: "bundle size", Alternative: "ky", Reason: "3 KB gzipped, modern fetch wrapper, retry built-in", Confidence: 0.8},
		{Library: "axios", Task: "tree-shaking", Alternative: "ofetch", Reason: "Nuxt ecosystem, auto-retry, 4 KB", Confidence: 0.75},
		{Library: "express", Task: "performance", Alternative: "fastify", Reason: "2x throughput, schema-based validation, plugin architecture", Confidence: 0.85},
		{Library: "react", Task: "bundle size", Alternative: "preact", Reason: "3 KB gzipped, same API + compat layer", Confidence: 0.7},
		{Library: "leaflet", Task: "10k polygons", Alternative: "MapLibre GL", Reason: "WebGL rendering handles 100k+ features; Leaflet DOM-based chokes at ~5k", Confidence: 0.95},
		{Library: "leaflet", Task: "large dataset", Alternative: "MapLibre GL", Reason: "Vector tiles + GPU acceleration for massive datasets", Confidence: 0.9},
		{Library: "d3", Task: "simple charts", Alternative: "chart.js", Reason: "canvas-based, 60 KB, zero config for standard charts", Confidence: 0.8},
		{Library: "d3", Task: "complex visualization", Alternative: "Observable Plot", Reason: "grammar of graphics, less boilerplate than raw D3", Confidence: 0.7},

		// Go
		{Library: "logrus", Task: "structured logging", Alternative: "slog (stdlib)", Reason: "Go 1.21+ stdlib, zero deps, slog.Handler for customization", Confidence: 0.9},
		{Library: "logrus", Task: "performance", Alternative: "zerolog", Reason: "zero allocation JSON, 10x faster than logrus", Confidence: 0.85},
		{Library: "github.com/pkg/errors", Task: "error wrapping", Alternative: "fmt.Errorf (stdlib)", Reason: "Go 1.13+ has %w, no external dep needed", Confidence: 0.9},
		{Library: "gorilla/mux", Task: "routing", Alternative: "stdlib net/http (Go 1.22+)", Reason: "Go 1.22 added method-based routing, mux is redundant for most APIs", Confidence: 0.85},
		{Library: "go-sqlite3", Task: "no CGO", Alternative: "modernc.org/sqlite", Reason: "pure Go, zero CGO, same SQLite engine via transpilation", Confidence: 0.95},
		{Library: "sqlite3", Task: "no CGO", Alternative: "modernc.org/sqlite", Reason: "pure Go, zero CGO, same SQLite engine via transpilation", Confidence: 0.95},

		// Python
		{Library: "requests", Task: "modern HTTP", Alternative: "httpx", Reason: "async support, HTTP/2, same requests-like API", Confidence: 0.85},
		{Library: "flask", Task: "async support", Alternative: "fastapi", Reason: "native async, auto-generated OpenAPI docs, Pydantic validation", Confidence: 0.8},
		{Library: "pandas", Task: "memory efficiency", Alternative: "polars", Reason: "Rust backend, 5-10x less memory, lazy evaluation", Confidence: 0.85},

		// Rust
		{Library: "reqwest", Task: "async HTTP", Alternative: "ureq", Reason: "blocking-first, simpler API for CLI tools, smaller binary", Confidence: 0.7},

		// General
		{Library: "JWT", Task: "session management", Alternative: "opaque tokens + Redis", Reason: "JWTs can't be revoked; opaque tokens are simpler and more secure", Confidence: 0.8},
		{Library: "MongoDB", Task: "document storage", Alternative: "PostgreSQL (JSONB)", Reason: "ACID, joins, full-text search, JSONB for documents — one DB instead of two", Confidence: 0.75},
		{Library: "Redis", Task: "caching", Alternative: "Valkey", Reason: "open-source Redis fork, BSD license, drop-in replacement", Confidence: 0.9},
	}
}
