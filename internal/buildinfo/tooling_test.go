package buildinfo

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Keep development checks runnable without the runtimes removed in 1.0.14.
// Historical conversation fixtures may still describe other people's JS projects;
// those strings are not executable dependencies of this repository.
func TestNoNodeDevelopmentToolchain(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{"package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "node_modules"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("unexpected Node development artifact %s (stat: %v)", name, err)
		}
	}
	commonJS := regexp.MustCompile(`\brequire\s*\(|\bmodule\.exports\b|["']node:`)
	nodeCommand := regexp.MustCompile(`(?m)^\s*(?:-\s+)?(?:run:\s*)?(?:node|npm|npx)(?:\s|$)`)
	for _, dir := range []string{"scripts", "test", "docs/readme", ".github/workflows", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(dir)), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			ext := filepath.Ext(path)
			goTest := strings.HasSuffix(path, "_test.go")
			developmentSource := dir != "internal"
			if developmentSource && ext == ".cjs" {
				t.Errorf("CommonJS development script remains: %s", path)
			}
			if !goTest && !(developmentSource && (ext == ".js" || ext == ".yml" || ext == ".yaml")) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if goTest {
				checkTestRuntimeCommands(t, path, data)
			}
			if ext == ".js" && commonJS.Match(data) {
				t.Errorf("Node/CommonJS API in browser development module: %s", path)
			}
			if dir == ".github/workflows" {
				// All current workflows use shell steps; even checkout/setup/artifact
				// JavaScript actions would bring back a Node runtime dependency.
				if regexp.MustCompile(`(?m)^\s*(?:-\s+)?uses:`).Match(data) || nodeCommand.Match(data) {
					t.Errorf("workflow reintroduces an action/runtime instead of native shell/Go: %s", path)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("inspect %s: %v", dir, err)
		}
	}
}

func checkTestRuntimeCommands(t *testing.T, path string, source []byte) {
	t.Helper()
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, path, source, 0)
	if err != nil {
		t.Fatalf("parse test %s: %v", path, err)
	}
	execNames := make(map[string]bool)
	for _, imp := range file.Imports {
		if imp.Path.Value == `"os/exec"` {
			name := "exec"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			execNames[name] = true
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || !execNames[pkg.Name] {
			return true
		}
		arg := 0
		switch selector.Sel.Name {
		case "CommandContext":
			arg = 1
		case "Command", "LookPath":
		default:
			return true
		}
		if len(call.Args) <= arg {
			return true
		}
		literal, ok := call.Args[arg].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil {
			switch strings.ToLower(value) {
			case "node", "node.exe", "npm", "npm.cmd", "npx", "npx.cmd":
				t.Errorf("test executes Node tooling at %s", positions.Position(call.Pos()))
			}
		}
		return true
	})
}
