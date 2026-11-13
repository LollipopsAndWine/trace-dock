// Command trace-dock-release builds the supported standalone release targets.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type target struct{ GOOS, GOARCH, Suffix string }
type releaseConfig struct {
	Version            string    `json:"version"`
	DefaultServerURL   string    `json:"default_server_url"`
	AutoIncrementPatch bool      `json:"auto_increment_patch"`
	Maintainer         string    `json:"maintainer,omitempty"`
	License            string    `json:"license,omitempty"`
	NPM                npmConfig `json:"npm"`
}

var targets = []target{{"linux", "amd64", ""}, {"linux", "arm64", ""}, {"darwin", "amd64", ""}, {"darwin", "arm64", ""}, {"windows", "amd64", ".exe"}}
var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func main() {
	ci := flag.Bool("ci", false, "use the unique vX.Y.Z tag at HEAD, preserving release.json")
	flag.Parse()
	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*ci); err != nil {
		fmt.Fprintln(os.Stderr, "trace-dock-release:", err)
		os.Exit(1)
	}
}

func run(ci bool) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	configName := os.Getenv("RELEASE_CONFIG")
	if configName == "" {
		configName = "release.json"
	}
	configPath := configName
	if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(root, configPath)
	}
	b, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read release config: %w", err)
	}
	var cfg releaseConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("invalid release config: %w", err)
	}
	tag, commit := "", ""
	if ci {
		tags, err := exec.Command("git", "tag", "--points-at", "HEAD", "--list", "v*").Output()
		if err != nil {
			return fmt.Errorf("read release tags at HEAD: %w", err)
		}
		tag, err = selectReleaseTag(string(tags))
		if err != nil {
			return err
		}
		cfg.Version = strings.TrimPrefix(tag, "v")
		cfg.AutoIncrementPatch = false
		revision, err := exec.Command("git", "rev-parse", "HEAD").Output()
		if err != nil {
			return fmt.Errorf("read release commit: %w", err)
		}
		commit = strings.TrimSpace(string(revision))
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}
	cfg.NPM = cfg.NPM.defaults()
	if err := cfg.NPM.validate(); err != nil {
		return err
	}
	version := cfg.Version
	next := ""
	if cfg.AutoIncrementPatch {
		next, err = nextVersion(version)
		if err != nil {
			return err
		}
	}
	outputName := os.Getenv("DIST_DIR")
	if outputName == "" {
		outputName = "dist"
	}
	output := outputName
	if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		return err
	}
	toolchain := os.Getenv("GOTOOLCHAIN")
	if toolchain == "" {
		toolchain = "go1.26.5"
	}
	goExecutable := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goExecutable += ".exe"
	}
	for _, t := range targets {
		name := "trace-dock-" + t.GOOS + "-" + t.GOARCH + t.Suffix
		cmd := exec.Command(goExecutable, "build", "-trimpath", "-ldflags", "-s -w -X main.version="+version+" -X github.com/trace-dock/trace-dock/internal/app.defaultServer="+cfg.DefaultServerURL, "-o", filepath.Join(output, name), "./cmd/trace-dock")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN="+toolchain, "CGO_ENABLED=0", "GOOS="+t.GOOS, "GOARCH="+t.GOARCH)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("build %s/%s: %w", t.GOOS, t.GOARCH, err)
		}
		if err := archiveBinary(output, name, t); err != nil {
			return err
		}
		if err := nativePackages(output, cfg, t); err != nil {
			return fmt.Errorf("package %s/%s: %w", t.GOOS, t.GOARCH, err)
		}
	}
	npmFiles, err := buildNPMPackages(output, cfg)
	if err != nil {
		return fmt.Errorf("package npm: %w", err)
	}
	var sums strings.Builder
	for _, t := range targets {
		name := "trace-dock-" + t.GOOS + "-" + t.GOARCH + t.Suffix
		for _, artifact := range append([]string{name, archiveName(t)}, nativeNames(t)...) {
			digest, err := hashFile(filepath.Join(output, artifact))
			if err != nil {
				return err
			}
			sums.WriteString(digest + "  " + artifact + "\n")
		}
	}
	for _, artifact := range npmFiles {
		digest, err := hashFile(filepath.Join(output, filepath.FromSlash(artifact)))
		if err != nil {
			return err
		}
		sums.WriteString(digest + "  " + artifact + "\n")
	}
	metadata, err := json.MarshalIndent(map[string]any{"version": version, "tag": tag, "commit": commit, "default_server_url": cfg.DefaultServerURL}, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(output, "RELEASE.json"), append(metadata, '\n'), 0644); err != nil {
		return err
	}
	digest, err := hashFile(filepath.Join(output, "RELEASE.json"))
	if err != nil {
		return err
	}
	sums.WriteString(digest + "  RELEASE.json\n")
	if err := os.WriteFile(filepath.Join(output, "SHA256SUMS"), []byte(sums.String()), 0600); err != nil {
		return err
	}
	if ci {
		// Gitee Go reads this file after the build step and exports these parameters
		// to subsequent stages. No undocumented tag environment variable is needed.
		params, err := os.OpenFile("GITEE_PARAMS", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		_, writeErr := fmt.Fprintf(params, "TRACE_DOCK_RELEASE_TAG=%s\n", tag)
		closeErr := params.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Printf("Built release %s from %s; release.json unchanged\n", tag, commit)
	} else if cfg.AutoIncrementPatch {
		// Preserve any extra release configuration fields while updating the version.
		var updated map[string]json.RawMessage
		if err := json.Unmarshal(b, &updated); err != nil {
			return err
		}
		updated["version"], _ = json.Marshal(next)
		if err := writeConfig(configPath, updated); err != nil {
			return err
		}
		fmt.Printf("Built release %s; next release version is %s\n", version, next)
	} else {
		fmt.Printf("Built release %s\n", version)
	}
	return nil
}

func validateConfig(cfg releaseConfig) error {
	if !semver.MatchString(cfg.Version) {
		return fmt.Errorf("release version must be X.Y.Z with no leading zeros")
	}
	u, err := url.Parse(cfg.DefaultServerURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(cfg.DefaultServerURL, "\r\n\t \"'") {
		return fmt.Errorf("release.json default_server_url must be an HTTPS URL without credentials, query or fragment")
	}
	return nil
}

func nextVersion(version string) (string, error) {
	match := semver.FindStringSubmatch(version)
	if match == nil {
		return "", fmt.Errorf("invalid version: %s", version)
	}
	patch, err := strconv.ParseUint(match[3], 10, 64)
	if err != nil || patch == ^uint64(0) {
		return "", fmt.Errorf("patch version is too large to increment")
	}
	return match[1] + "." + match[2] + "." + strconv.FormatUint(patch+1, 10), nil
}

func selectReleaseTag(output string) (string, error) {
	tags := []string{}
	for _, tag := range strings.Fields(output) {
		if strings.HasPrefix(tag, "v") && semver.MatchString(tag[1:]) {
			tags = append(tags, tag)
		}
	}
	if len(tags) != 1 {
		return "", fmt.Errorf("CI requires exactly one vX.Y.Z tag at HEAD (found %d); enable tag checkout/fetch in Gitee Go", len(tags))
	}
	return tags[0], nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeConfig(path string, cfg any) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".release-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func archiveName(t target) string {
	ext := ".tar.gz"
	if t.GOOS == "windows" {
		ext = ".zip"
	}
	return "trace-dock-" + t.GOOS + "-" + t.GOARCH + ext
}

func archiveBinary(output, binary string, t target) error {
	data, err := os.Open(filepath.Join(output, binary))
	if err != nil {
		return err
	}
	defer data.Close()
	info, err := data.Stat()
	if err != nil {
		return err
	}
	if t.GOOS == "windows" {
		out, err := os.Create(filepath.Join(output, archiveName(t)))
		if err != nil {
			return err
		}
		zw := zip.NewWriter(out)
		header := &zip.FileHeader{Name: "trace-dock.exe", Method: zip.Deflate}
		header.SetMode(0755)
		h, err := zw.CreateHeader(header)
		if err == nil {
			_, err = io.Copy(h, data)
		}
		if closeErr := zw.Close(); err == nil {
			err = closeErr
		}
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		return err
	}
	out, err := os.Create(filepath.Join(output, archiveName(t)))
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	err = tw.WriteHeader(&tar.Header{Name: "trace-dock", Mode: 0755, Size: info.Size(), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg, Format: tar.FormatPAX})
	if err == nil {
		_, err = io.Copy(tw, data)
	}
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gz.Close(); err == nil {
		err = closeErr
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}
