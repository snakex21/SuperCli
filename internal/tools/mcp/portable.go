package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	PortableDirName = "mcp"
	ManifestName    = "manifest.toml"
)

// Manifest describes one self-contained MCP package stored below
// <dataDir>/mcp/<package>/manifest.toml. All paths may use the portable
// placeholders ${MCP_DIR}, ${DATA_DIR}, and ${SUPERCLI_DIR}.
type Manifest struct {
	Schema      int               `toml:"schema"`
	Name        string            `toml:"name"`
	Version     string            `toml:"version"`
	Description string            `toml:"description"`
	Command     string            `toml:"command"`
	Args        []string          `toml:"args"`
	Env         map[string]string `toml:"env"`
	Cwd         string            `toml:"cwd"`
	Tags        []string          `toml:"tags"`
	Platforms   []string          `toml:"platforms"`
	Requires    []Requirement     `toml:"requires"`
	Enabled     *bool             `toml:"enabled"`
}

// Requirement describes an external host application or runtime. Candidates
// are checked in order and may be executable names, absolute/relative paths,
// or glob patterns. This lets a portable Blender/Godot bridge explain that its
// host application is missing without failing the whole CLI.
type Requirement struct {
	Name       string   `toml:"name"`
	Candidates []string `toml:"candidates"`
	Optional   bool     `toml:"optional"`
}

type PortablePackage struct {
	ID          string
	Dir         string
	Manifest    Manifest
	Name        string
	Enabled     bool
	Available   bool
	Error       string
	Missing     []string
	Resolved    ServerConfig
	ManifestRel string
}

// DiscoverPortable finds portable MCP packages without starting a process.
// A broken package is returned with Available=false so diagnostics can show it;
// one malformed package never disables the remaining workspace.
func DiscoverPortable(dataDir string) ([]PortablePackage, error) {
	root := filepath.Join(dataDir, PortableDirName)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return []PortablePackage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mcp portable workspace: %w", err)
	}
	out := make([]PortablePackage, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pkg := PortablePackage{ID: entry.Name(), Dir: filepath.Join(root, entry.Name()), Enabled: true}
		manifestPath := filepath.Join(pkg.Dir, ManifestName)
		pkg.ManifestRel = filepath.Join(PortableDirName, entry.Name(), ManifestName)
		if _, err := toml.DecodeFile(manifestPath, &pkg.Manifest); err != nil {
			if os.IsNotExist(err) {
				continue // ordinary folders below mcp/ are not implicitly executable
			}
			pkg.Error = "manifest: " + err.Error()
			out = append(out, pkg)
			continue
		}
		if pkg.Manifest.Schema != 0 && pkg.Manifest.Schema != 1 {
			pkg.Error = fmt.Sprintf("unsupported manifest schema: %d", pkg.Manifest.Schema)
			out = append(out, pkg)
			continue
		}
		pkg.Name = strings.TrimSpace(pkg.Manifest.Name)
		if pkg.Name == "" {
			pkg.Name = pkg.ID
		}
		if pkg.Manifest.Enabled != nil {
			pkg.Enabled = *pkg.Manifest.Enabled
		}
		if !pkg.Enabled {
			pkg.Error = "disabled by manifest"
			out = append(out, pkg)
			continue
		}
		if !supportsCurrentPlatform(pkg.Manifest.Platforms) {
			pkg.Error = fmt.Sprintf("unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
			out = append(out, pkg)
			continue
		}
		resolved, resolveErr := resolvePortableConfig(dataDir, pkg)
		pkg.Resolved = resolved
		if resolveErr != nil {
			pkg.Error = resolveErr.Error()
			out = append(out, pkg)
			continue
		}
		for _, requirement := range pkg.Manifest.Requires {
			if requirementAvailable(dataDir, pkg.Dir, requirement) {
				continue
			}
			label := strings.TrimSpace(requirement.Name)
			if label == "" {
				label = strings.Join(requirement.Candidates, " | ")
			}
			if !requirement.Optional {
				pkg.Missing = append(pkg.Missing, label)
			}
		}
		if len(pkg.Missing) > 0 {
			pkg.Error = "missing: " + strings.Join(pkg.Missing, ", ")
			out = append(out, pkg)
			continue
		}
		pkg.Available = true
		out = append(out, pkg)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// LoadWorkspace merges portable packages with explicit config.toml entries.
// Explicit entries win by name, preserving today's configuration semantics.
func LoadWorkspace(dataDir string, explicit map[string]ServerConfig) (map[string]ServerConfig, []PortablePackage, error) {
	if err := SeedDetectedPortableProfiles(dataDir); err != nil {
		return nil, nil, fmt.Errorf("seed portable profiles: %w", err)
	}
	packages, err := DiscoverPortable(dataDir)
	if err != nil {
		return nil, nil, err
	}
	configs := make(map[string]ServerConfig, len(packages)+len(explicit))
	for _, pkg := range packages {
		if pkg.Available {
			configs[pkg.Name] = pkg.Resolved
		}
	}
	for name, cfg := range explicit {
		configs[name] = resolveExplicitConfig(dataDir, name, cfg)
	}
	return configs, packages, nil
}

func resolvePortableConfig(dataDir string, pkg PortablePackage) (ServerConfig, error) {
	manifest := pkg.Manifest
	commandText := expandPortable(manifest.Command, dataDir, pkg.Dir)
	command, err := resolveCommand(commandText, pkg.Dir)
	if err != nil {
		return ServerConfig{}, err
	}
	dir := pkg.Dir
	if strings.TrimSpace(manifest.Cwd) != "" {
		dir = expandPortable(manifest.Cwd, dataDir, pkg.Dir)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(pkg.Dir, dir)
		}
		dir = filepath.Clean(dir)
		if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
			return ServerConfig{}, fmt.Errorf("working directory not found: %s", dir)
		}
	}
	args := make([]string, len(manifest.Args))
	for i, arg := range manifest.Args {
		args[i] = expandPortable(arg, dataDir, pkg.Dir)
	}
	env := make(map[string]string, len(manifest.Env))
	for key, value := range manifest.Env {
		env[key] = expandPortable(value, dataDir, pkg.Dir)
	}
	return ServerConfig{
		Command: command, Args: args, Env: env, Dir: dir,
		Description: manifest.Description, Portable: true, PackageDir: pkg.Dir,
		PackageID: pkg.ID, Tags: append([]string(nil), manifest.Tags...),
	}, nil
}

func resolveExplicitConfig(dataDir, name string, cfg ServerConfig) ServerConfig {
	pkgDir := filepath.Join(dataDir, PortableDirName, name)
	cfg.Args = append([]string(nil), cfg.Args...)
	env := make(map[string]string, len(cfg.Env))
	for key, value := range cfg.Env {
		env[key] = value
	}
	cfg.Env = env
	cfg.Command = expandPortable(cfg.Command, dataDir, pkgDir)
	for i := range cfg.Args {
		cfg.Args[i] = expandPortable(cfg.Args[i], dataDir, pkgDir)
	}
	for key, value := range cfg.Env {
		cfg.Env[key] = expandPortable(value, dataDir, pkgDir)
	}
	if cfg.Dir == "" {
		cfg.Dir = filepath.Dir(dataDir)
	}
	return cfg
}

func expandPortable(value, dataDir, pkgDir string) string {
	replacements := map[string]string{
		"${MCP_DIR}":      pkgDir,
		"${DATA_DIR}":     dataDir,
		"${SUPERCLI_DIR}": filepath.Dir(dataDir),
		"${OS}":           runtime.GOOS,
		"${ARCH}":         runtime.GOARCH,
	}
	for placeholder, replacement := range replacements {
		value = strings.ReplaceAll(value, placeholder, replacement)
	}
	return os.ExpandEnv(value)
}

func resolveCommand(command, pkgDir string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("command is empty")
	}
	if filepath.IsAbs(command) {
		if fileExists(command) {
			return filepath.Clean(command), nil
		}
		return "", fmt.Errorf("command not found: %s", command)
	}
	local := filepath.Join(pkgDir, command)
	if fileExists(local) {
		abs, _ := filepath.Abs(local)
		return filepath.Clean(abs), nil
	}
	if runtime.GOOS == "windows" && filepath.Ext(local) == "" && fileExists(local+".exe") {
		abs, _ := filepath.Abs(local + ".exe")
		return filepath.Clean(abs), nil
	}
	if strings.ContainsAny(command, `/\`) {
		return "", fmt.Errorf("command not found: %s", local)
	}
	if found, err := exec.LookPath(command); err == nil {
		return found, nil
	}
	return "", fmt.Errorf("command not found in package or PATH: %s", command)
}

func supportsCurrentPlatform(platforms []string) bool {
	if len(platforms) == 0 {
		return true
	}
	want := runtime.GOOS + "/" + runtime.GOARCH
	for _, platform := range platforms {
		platform = strings.ToLower(strings.TrimSpace(platform))
		if platform == runtime.GOOS || platform == want || platform == "*" {
			return true
		}
	}
	return false
}

func requirementAvailable(dataDir, pkgDir string, req Requirement) bool {
	if len(req.Candidates) == 0 {
		return req.Optional
	}
	for _, raw := range req.Candidates {
		candidate := expandPortable(raw, dataDir, pkgDir)
		if strings.ContainsAny(candidate, "*?[") {
			if matches, _ := filepath.Glob(candidate); len(matches) > 0 {
				return true
			}
			continue
		}
		if filepath.IsAbs(candidate) || strings.ContainsAny(candidate, `/\`) {
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(pkgDir, candidate)
			}
			if fileExists(candidate) {
				return true
			}
			continue
		}
		if fileExists(filepath.Join(pkgDir, candidate)) {
			return true
		}
		if _, err := exec.LookPath(candidate); err == nil {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
