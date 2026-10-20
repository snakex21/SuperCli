package agent

import (
	"crypto/sha256"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Coverage deliberately recognizes a small, auditable Go test grammar. Unknown
// flags/shell scripts keep exact-command recovery; they never imply a superset.
type verificationScope struct {
	root, executable, mode, run string
	environment                 [sha256.Size]byte
	packages                    []string
	count                       int
	goTest                      bool
	failedPackages              []string
	passedPackages              map[string]bool
	passedTests                 map[string]bool
}

func describeVerification(command []string, root string, environment [sha256.Size]byte) verificationScope {
	s := verificationScope{root: filepath.Clean(root), environment: environment, count: 1}
	if len(command) < 2 || command[1] != "test" {
		return s
	}
	executable := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(command[0], "\\", "/"))), ".exe")
	if executable != "go" {
		return s
	}
	s.executable = command[0]
	var modes []string
	for i := 2; i < len(command); i++ {
		arg := command[i]
		if !strings.HasPrefix(arg, "-") {
			if arg == "" || strings.Contains(arg, "@") {
				return s
			}
			s.packages = append(s.packages, arg)
			continue
		}
		name, value, assigned := strings.Cut(arg, "=")
		switch name {
		case "-v":
			if assigned && value != "true" && value != "false" {
				return s
			}
		case "-json":
			// JSON event coverage needs event-level package/test attribution.
			// Keep exact-command recovery instead of treating JSON as plain logs.
			return s
		case "-race", "-short":
			if !assigned {
				value = "true"
			}
			if value != "true" && value != "false" {
				return s
			}
			modes = append(modes, name+"="+value)
		case "-run", "-count", "-timeout", "-p", "-tags", "-shuffle", "-mod", "-modfile", "-overlay", "-gcflags", "-ldflags", "-covermode", "-coverpkg", "-parallel":
			if !assigned {
				i++
				if i >= len(command) {
					return s
				}
				value = command[i]
			}
			switch name {
			case "-run":
				s.run = value
			case "-count":
				if count, err := strconv.Atoi(value); err != nil || count <= 0 {
					return s
				} else {
					s.count = count
				}
			case "-timeout", "-p": // Execution budgets do not narrow test selection.
			default:
				modes = append(modes, name+"="+value)
			}
		default:
			return s
		}
	}
	if len(s.packages) == 0 {
		s.packages = []string{"."}
	}
	sort.Strings(modes)
	s.mode, s.goTest = strings.Join(modes, "\x00"), true
	return s
}

// revalidatesSuccessful is only for a previously successful check invalidated
// by a later edit, not an actual failed test. A full selection can replace its
// old filter, but must still execute every previously observed package in the
// same root, mode and environment. Narrower selections retain the old filter.
func (s verificationScope) revalidatesSuccessful(previous verificationScope) bool {
	required := previous
	required.failedPackages = append([]string(nil), previous.failedPackages...)
	for pkg, passed := range previous.passedPackages {
		if passed {
			required.failedPackages = append(required.failedPackages, pkg)
		}
	}
	if s.run == "" {
		required.run = ""
	}
	return s.covers(required)
}

func (s verificationScope) covers(failed verificationScope) bool {
	if !s.goTest || !failed.goTest || s.root != failed.root || s.executable != failed.executable || s.environment != failed.environment || s.mode != failed.mode || s.count < failed.count {
		return false
	}
	if s.run != failed.run {
		// Full selection may replace a literal filtered test only when execution
		// explicitly reports that test PASS. Arbitrary regex implication is unsafe.
		if s.run != "" || !s.passedTests[strings.TrimSuffix(strings.TrimPrefix(failed.run, "^"), "$")] {
			return false
		}
	}
	for _, target := range failed.packages {
		covered := false
		for _, candidate := range s.packages {
			if goPackageContains(candidate, target) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	// A successful process alone cannot prove that a previously failed package
	// was included: require that executor's package-level successful result.
	if len(failed.failedPackages) == 0 {
		return false
	}
	for _, pkg := range failed.failedPackages {
		if !s.passedPackages[pkg] {
			return false
		}
	}
	return true
}

func goPackageContains(parent, child string) bool {
	if parent == child {
		return true
	}
	if !strings.HasSuffix(parent, "/...") {
		return false
	}
	root := strings.TrimSuffix(parent, "/...")
	if root == "." {
		return child == "." || strings.HasPrefix(child, "./")
	}
	return child == root || strings.HasPrefix(child, root+"/")
}

func (s *verificationScope) observeGoOutput(stdout string) {
	s.passedPackages, s.passedTests = make(map[string]bool), make(map[string]bool)
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "FAIL" {
			s.failedPackages = append(s.failedPackages, fields[1])
		}
		if len(fields) >= 2 && fields[0] == "ok" && !strings.Contains(line, "[no tests to run]") {
			s.passedPackages[fields[1]] = true
		}
		if len(fields) >= 3 && fields[0] == "---" && fields[1] == "PASS:" {
			s.passedTests[fields[2]] = true
		}
	}
}

func verificationSkipped(stdout, stderr string) bool {
	output := stdout + "\n" + stderr
	return strings.Contains(output, "no tests to run") || strings.Contains(output, "no tests ran") ||
		strings.Contains(output, "--- SKIP:") ||
		(strings.Contains(output, "[no test files]") && !goTestExecuted(stdout))
}

func goTestExecuted(stdout string) bool {
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "ok" && !strings.Contains(line, "[no tests to run]") {
			return true
		}
	}
	return false
}

func (s verificationScope) label() string {
	if s.goTest {
		return "go test " + strings.Join(s.packages, " ") + " (compatible mode/environment required)"
	}
	return "verification command"
}
