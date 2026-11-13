package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/deploymenttheory/go-macos-pkg/pkg/flatpkg"
	"github.com/goreleaser/nfpm/v2"
	_ "github.com/goreleaser/nfpm/v2/deb"
	"github.com/goreleaser/nfpm/v2/files"
	_ "github.com/goreleaser/nfpm/v2/rpm"
)

// Packages contain only this binary. No hooks touch HOME, workspaces or data.
func nativeNames(t target) []string {
	base := "trace-dock-" + t.GOOS + "-" + t.GOARCH
	switch t.GOOS {
	case "linux":
		return []string{base + ".deb", base + ".rpm"}
	case "darwin":
		return []string{base + ".pkg"}
	default:
		return nil
	}
}

func nativePackages(output string, cfg releaseConfig, t target) error {
	if t.GOOS == "linux" {
		for _, format := range []string{"deb", "rpm"} {
			if err := linuxPackage(output, cfg, t, format); err != nil {
				return err
			}
		}
	} else if t.GOOS == "darwin" {
		return macPackage(output, cfg, t)
	}
	return nil
}

func linuxPackage(output string, cfg releaseConfig, t target, format string) error {
	maintainer := cfg.Maintainer
	if maintainer == "" {
		maintainer = "Trace Dock maintainers"
	}
	license := cfg.License
	if license == "" {
		license = "UNLICENSED"
	}
	info := nfpm.WithDefaults(&nfpm.Info{
		Name: "trace-dock", Arch: t.GOARCH, Platform: "linux", Version: cfg.Version,
		Section: "utils", Priority: "optional", Maintainer: maintainer,
		Description: "Collect selected Codex or Claude sessions and upload them to Trace Dock.",
		Vendor:      "Trace Dock", License: license, DisableGlobbing: true, MTime: time.Unix(0, 0).UTC(),
		Overridables: nfpm.Overridables{Contents: files.Contents{&files.Content{
			Source:      filepath.Join(output, "trace-dock-linux-"+t.GOARCH),
			Destination: "/usr/bin/trace-dock", Type: files.TypeFile,
			FileInfo: &files.ContentFileInfo{Owner: "root", Group: "root", Mode: 0755, MTime: time.Unix(0, 0).UTC()},
		}}},
	})
	if err := nfpm.Validate(info); err != nil {
		return err
	}
	packager, err := nfpm.Get(format)
	if err != nil {
		return err
	}
	out, err := os.Create(filepath.Join(output, "trace-dock-linux-"+t.GOARCH+"."+format))
	if err != nil {
		return err
	}
	packageErr := packager.Package(info, out)
	closeErr := out.Close()
	if packageErr != nil {
		return packageErr
	}
	return closeErr
}

func macPackage(output string, cfg releaseConfig, t target) error {
	if t.GOARCH != "amd64" && t.GOARCH != "arm64" {
		return fmt.Errorf("unsupported macOS architecture %s", t.GOARCH)
	}
	staging, err := os.MkdirTemp(output, ".pkg-staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	root := filepath.Join(staging, "root")
	bin := filepath.Join(root, "usr", "local", "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		return err
	}
	src, err := os.Open(filepath.Join(output, "trace-dock-darwin-"+t.GOARCH))
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(filepath.Join(bin, "trace-dock"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	component := filepath.Join(staging, "trace-dock-component.pkg")
	f, err := os.Create(component)
	if err != nil {
		return err
	}
	_, buildErr := flatpkg.BuildComponent(flatpkg.ComponentOptions{
		Root: root, Identifier: "org.trace-dock.cli", Version: cfg.Version,
		InstallLocation: "/", Epoch: time.Unix(0, 0).UTC(),
		Ownership: flatpkg.OwnershipRecommended, Auth: "root",
		FileModes: map[string]uint32{".": 0755, "./usr": 0755, "./usr/local": 0755, "./usr/local/bin": 0755, "./usr/local/bin/trace-dock": 0755},
	}, f)
	closeErr = f.Close()
	if buildErr != nil {
		return buildErr
	}
	if closeErr != nil {
		return closeErr
	}
	arch := "x86_64"
	if t.GOARCH == "arm64" {
		arch = "arm64"
	}
	f, err = os.Create(filepath.Join(output, "trace-dock-darwin-"+t.GOARCH+".pkg"))
	if err != nil {
		return err
	}
	_, buildErr = flatpkg.BuildProduct(flatpkg.ProductOptions{
		Packages: []string{component}, Title: "Trace Dock CLI", ProductID: "org.trace-dock.cli", ProductVersion: cfg.Version,
		HostArchitectures: []string{arch}, Epoch: time.Unix(0, 0).UTC(),
	}, f)
	closeErr = f.Close()
	if buildErr != nil {
		return buildErr
	}
	return closeErr
}
