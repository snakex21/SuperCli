// Package releasebundle produces portable, public release assets without user data.
package releasebundle

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"supercli/internal/system/updater"
)

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var bundlePattern = regexp.MustCompile(`^supercli_([0-9]+\.[0-9]+\.[0-9]+)_(windows|linux|darwin)_(amd64|arm64)\.zip$`)

type Options struct{ Version, OS, Arch, CLI, GUI, Source, Output string }

func validVersion(v string) error {
	if !versionPattern.MatchString(v) {
		return fmt.Errorf("expected a stable x.y.z version")
	}
	return nil
}
func Pack(opts Options) (string, error) {
	if err := validVersion(opts.Version); err != nil {
		return "", err
	}
	name := fmt.Sprintf("supercli_%s_%s_%s.zip", opts.Version, opts.OS, opts.Arch)
	if !bundlePattern.MatchString(name) {
		return "", fmt.Errorf("unsupported release platform")
	}
	if err := os.MkdirAll(opts.Output, 0755); err != nil {
		return "", err
	}
	destination := filepath.Join(opts.Output, name)
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return "", err
	}
	success := false
	defer func() {
		out.Close()
		if !success {
			os.Remove(destination)
		}
	}()
	archive := zip.NewWriter(out)
	ext := ""
	if opts.OS == "windows" {
		ext = ".exe"
	}
	add := func(source, name string, mode fs.FileMode) error {
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("release asset is not a regular file: %s", source)
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		defer input.Close()
		header := &zip.FileHeader{Name: filepath.ToSlash(name), Method: zip.Deflate}
		header.SetMode(mode)
		output, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = io.Copy(output, input)
		return err
	}
	for _, binary := range []struct{ source, name string }{{opts.CLI, "supercli" + ext}, {opts.GUI, "supercli-web" + ext}} {
		if err := add(binary.source, binary.name, 0755); err != nil {
			return "", err
		}
	}
	// Public files are explicitly selected. Never walk the application data root.
	for _, name := range []string{"README.md", "LICENSE", "THIRD_PARTY_NOTICES.md"} {
		if err := add(filepath.Join(opts.Source, name), name, 0644); err != nil {
			return "", err
		}
	}
	docs := filepath.Join(opts.Source, "docs")
	err = filepath.WalkDir(docs, func(full string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(docs, full)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		if entry.IsDir() {
			if slash != "." && slash != "readme" && slash != "releases" && slash != "screenshots" && !strings.HasPrefix(slash, "screenshots/") {
				return filepath.SkipDir
			}
			return nil
		}
		extension := strings.ToLower(filepath.Ext(full))
		if extension != ".md" && !(strings.HasPrefix(slash, "screenshots/") && (extension == ".png" || extension == ".webp" || extension == ".jpg" || extension == ".jpeg")) {
			return nil
		}
		return add(full, "docs/"+slash, 0644)
	})
	if err != nil {
		return "", err
	}
	skills := "supercli-data/skills/builtin-skills.zip"
	if _, err := os.Stat(filepath.Join(opts.Source, filepath.FromSlash(skills))); err == nil {
		if err := add(filepath.Join(opts.Source, filepath.FromSlash(skills)), skills, 0644); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := archive.Close(); err != nil {
		return "", err
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	success = true
	return destination, nil
}
func Manifest(version, dir string) (updater.Manifest, string, error) {
	result := updater.Manifest{Version: version}
	if err := validVersion(version); err != nil {
		return result, "", err
	}
	files, err := filepath.Glob(filepath.Join(dir, "supercli_*.zip"))
	if err != nil {
		return result, "", err
	}
	if len(files) == 0 {
		return result, "", fmt.Errorf("no portable bundles found")
	}
	sort.Strings(files)
	sums := strings.Builder{}
	seen := map[string]bool{}
	for _, file := range files {
		name := filepath.Base(file)
		parts := bundlePattern.FindStringSubmatch(name)
		if len(parts) != 4 || parts[1] != version {
			return result, "", fmt.Errorf("unexpected bundle: %s", name)
		}
		platform := parts[2] + "/" + parts[3]
		if seen[platform] {
			return result, "", fmt.Errorf("duplicate platform")
		}
		seen[platform] = true
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() {
			return result, "", fmt.Errorf("invalid bundle file: %s", name)
		}
		input, err := os.Open(file)
		if err != nil {
			return result, "", err
		}
		hash := sha256.New()
		size, err := io.Copy(hash, input)
		closeErr := input.Close()
		if err != nil {
			return result, "", err
		}
		if closeErr != nil {
			return result, "", closeErr
		}
		digest := hex.EncodeToString(hash.Sum(nil))
		result.Assets = append(result.Assets, updater.Asset{OS: parts[2], Arch: parts[3], File: name, SHA256: digest, Size: size})
		fmt.Fprintf(&sums, "%s  %s\n", digest, name)
	}
	return result, sums.String(), nil
}
func WriteManifest(version, dir string) error {
	manifest, sums, err := Manifest(version, dir)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, updater.ManifestName), append(raw, '\n'), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0644)
}
