package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type npmConfig struct {
	Name     string `json:"name"`
	Registry string `json:"registry"`
	Access   string `json:"access"`
	Tag      string `json:"tag"`
}

func (c npmConfig) defaults() npmConfig {
	if c.Name == "" {
		c.Name = "@trace-dock/cli"
	}
	if c.Registry == "" {
		c.Registry = "https://registry.npmjs.org/"
	}
	if c.Access == "" {
		c.Access = "public"
	}
	if c.Tag == "" {
		c.Tag = "latest"
	}
	return c
}

var npmNamePattern = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
var npmTagPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]*$`)

func (c npmConfig) validate() error {
	if !npmNamePattern.MatchString(c.Name) || len(c.Name) > 190 {
		return fmt.Errorf("npm.name must be a valid npm package name owned by your account")
	}
	if c.Access != "public" && c.Access != "restricted" {
		return fmt.Errorf("npm.access must be public or restricted")
	}
	if !npmTagPattern.MatchString(c.Tag) {
		return fmt.Errorf("invalid npm.tag")
	}
	u, err := url.Parse(c.Registry)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("invalid npm.registry")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return fmt.Errorf("npm.registry must use HTTPS (loopback HTTP is allowed for testing)")
	}
	return nil
}

type npmPlatform struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Package string `json:"package"`
	Binary  string `json:"binary"`
	SHA256  string `json:"sha256"`
}
type npmPublishPackage struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	File      string `json:"file"`
	Integrity string `json:"integrity"`
}
type npmPublishPlan struct {
	Registry string              `json:"registry"`
	Access   string              `json:"access"`
	Tag      string              `json:"tag"`
	Packages []npmPublishPackage `json:"packages"`
}
type npmMember struct {
	Name   string
	Mode   int64
	Data   []byte
	Source string
}

func npmTarget(t target) (string, string) {
	osName, arch := t.GOOS, t.GOARCH
	if osName == "windows" {
		osName = "win32"
	}
	if arch == "amd64" {
		arch = "x64"
	}
	return osName, arch
}

func buildNPMPackages(output string, cfg releaseConfig) ([]string, error) {
	c := cfg.NPM.defaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	dir := filepath.Join(output, "npm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	license := cfg.License
	if license == "" {
		license = "UNLICENSED"
	}
	plan := npmPublishPlan{Registry: c.Registry, Access: c.Access, Tag: c.Tag}
	platforms := map[string]npmPlatform{}
	optional := map[string]string{}
	artifacts := []string{}
	addPackage := func(name, file string, members []npmMember) error {
		p := filepath.Join(dir, file)
		if err := writeNPMPackage(p, members); err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		h := sha512.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		plan.Packages = append(plan.Packages, npmPublishPackage{Name: name, Version: cfg.Version, File: file, Integrity: "sha512-" + base64.StdEncoding.EncodeToString(h.Sum(nil))})
		artifacts = append(artifacts, "npm/"+file)
		return nil
	}
	for _, t := range targets {
		osName, arch := npmTarget(t)
		suffix := osName + "-" + arch
		packageName := c.Name + "-" + suffix
		binary := "trace-dock" + t.Suffix
		source := filepath.Join(output, "trace-dock-"+t.GOOS+"-"+t.GOARCH+t.Suffix)
		digest, err := hashFile(source)
		if err != nil {
			return nil, err
		}
		platforms[suffix] = npmPlatform{OS: osName, Arch: arch, Package: packageName, Binary: binary, SHA256: digest}
		optional[packageName] = cfg.Version
		manifest := map[string]any{"name": packageName, "version": cfg.Version, "description": "Trace Dock binary for " + suffix, "os": []string{osName}, "cpu": []string{arch}, "license": license, "files": []string{"bin/" + binary}, "publishConfig": map[string]string{"registry": c.Registry, "access": c.Access, "tag": c.Tag}}
		data, _ := json.MarshalIndent(manifest, "", "  ")
		if err = addPackage(packageName, "trace-dock-"+suffix+".tgz", []npmMember{{Name: "package.json", Mode: 0644, Data: append(data, '\n')}, {Name: "bin/" + binary, Mode: 0755, Source: source}}); err != nil {
			return nil, err
		}
	}
	manifest := map[string]any{"name": c.Name, "version": cfg.Version, "description": "Collect selected Codex or Claude sessions with Trace Dock", "license": license, "bin": map[string]string{"trace-dock": "bin/trace-dock.cjs"}, "engines": map[string]string{"node": ">=18"}, "os": []string{"linux", "darwin", "win32"}, "cpu": []string{"x64", "arm64"}, "optionalDependencies": optional, "files": []string{"bin/", "platforms.json", "README.md"}, "publishConfig": map[string]string{"registry": c.Registry, "access": c.Access, "tag": c.Tag}}
	manifestJSON, _ := json.MarshalIndent(manifest, "", "  ")
	platformJSON, _ := json.MarshalIndent(platforms, "", "  ")
	readme := fmt.Sprintf("# Trace Dock CLI\n\nInstall: `npm install -g %s`\n\nRun: `trace-dock`\n\nUninstall: `npm uninstall -g %s` (keeps user configuration and collected data).\n\nNode.js 18+ is required; Go and Python are not required. Supports Linux x64/arm64, macOS x64/arm64, and Windows x64. Do not omit optional dependencies. This package has no install scripts.\n", c.Name, c.Name)
	if err := addPackage(c.Name, "trace-dock-cli.tgz", []npmMember{{Name: "package.json", Mode: 0644, Data: append(manifestJSON, '\n')}, {Name: "platforms.json", Mode: 0644, Data: append(platformJSON, '\n')}, {Name: "bin/trace-dock.cjs", Mode: 0755, Data: []byte(npmLauncher)}, {Name: "README.md", Mode: 0644, Data: []byte(readme)}}); err != nil {
		return nil, err
	}
	planJSON, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, "publish.json"), append(planJSON, '\n'), 0644); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, "publish.cjs"), []byte(npmPublisher), 0644); err != nil {
		return nil, err
	}
	return append(artifacts, "npm/publish.json", "npm/publish.cjs"), nil
}

func writeNPMPackage(file string, members []npmMember) error {
	out, err := os.Create(file)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		size := int64(len(m.Data))
		var src *os.File
		if m.Source != "" {
			src, err = os.Open(m.Source)
			if err != nil {
				break
			}
			var info os.FileInfo
			info, err = src.Stat()
			if err != nil {
				src.Close()
				break
			}
			size = info.Size()
		}
		err = tw.WriteHeader(&tar.Header{Name: "package/" + m.Name, Mode: m.Mode, Size: size, Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0), Format: tar.FormatPAX})
		if err == nil {
			if src != nil {
				_, err = io.Copy(tw, src)
			} else {
				_, err = tw.Write(m.Data)
			}
		}
		if src != nil {
			src.Close()
		}
		if err != nil {
			break
		}
	}
	for _, close := range []func() error{tw.Close, gz.Close, out.Close} {
		if e := close(); err == nil {
			err = e
		}
	}
	return err
}
