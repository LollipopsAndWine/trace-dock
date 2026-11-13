package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blakesmith/ar"
	"github.com/deploymenttheory/go-macos-pkg/pkg/flatpkg"
	"github.com/sassoftware/go-rpmutils"
)

func TestNativePackagesContainOnlyCLI(t *testing.T) {
	for _, target := range targets {
		if target.GOOS == "windows" {
			continue
		}
		t.Run(target.GOOS+"/"+target.GOARCH, func(t *testing.T) {
			dir := t.TempDir()
			name := "trace-dock-" + target.GOOS + "-" + target.GOARCH
			binary := []byte("test binary for " + target.GOOS + "/" + target.GOARCH + "\x00\xff")
			if err := os.WriteFile(filepath.Join(dir, name), binary, 0755); err != nil {
				t.Fatal(err)
			}
			cfg := releaseConfig{Version: "1.2.3"}
			if err := nativePackages(dir, cfg, target); err != nil {
				t.Fatal(err)
			}
			if target.GOOS == "linux" {
				verifyDeb(t, filepath.Join(dir, name+".deb"), target.GOARCH, binary)
				verifyRPM(t, filepath.Join(dir, name+".rpm"), target.GOARCH, binary)
			} else {
				verifyMacPkg(t, filepath.Join(dir, name+".pkg"), target.GOARCH, binary)
			}
		})
	}
}

func verifyDeb(t *testing.T, name, arch string, binary []byte) {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	reader := ar.NewReader(f)
	members := 0
	files := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		members++
		switch header.Name {
		case "debian-binary":
			data, err := io.ReadAll(reader)
			if err != nil || string(data) != "2.0\n" {
				t.Fatalf("bad deb format: %v", err)
			}
		case "control.tar.gz", "data.tar.gz":
			gz, err := gzip.NewReader(reader)
			if err != nil {
				t.Fatal(err)
			}
			tr := tar.NewReader(gz)
			for {
				h, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if h.Typeflag != tar.TypeReg {
					continue
				}
				data, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				if header.Name == "control.tar.gz" {
					base := path.Base(h.Name)
					if base != "control" && base != "md5sums" && base != "conffiles" {
						t.Fatalf("unexpected deb control script or file: %s", h.Name)
					}
					if base == "conffiles" && strings.TrimSpace(string(data)) != "" {
						t.Fatal("deb must not install user configuration")
					}
					if base == "control" && (!strings.Contains(string(data), "Version: 1.2.3\n") || !strings.Contains(string(data), "Architecture: "+arch+"\n")) {
						t.Fatalf("wrong deb identity: %s", data)
					}
				} else {
					files++
					if strings.TrimPrefix(path.Clean(h.Name), "/") != "usr/bin/trace-dock" || h.Mode != 0755 || !bytes.Equal(data, binary) {
						t.Fatalf("deb installs unexpected user file: %s", h.Name)
					}
				}
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unexpected deb member: %s", header.Name)
		}
	}
	if members != 3 || files != 1 {
		t.Fatalf("deb members=%d files=%d", members, files)
	}
}

func verifyRPM(t *testing.T, name, arch string, binary []byte) {
	t.Helper()
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := rpmutils.ReadRpm(f)
	if err != nil {
		t.Fatal(err)
	}
	expectedArch := "x86_64"
	if arch == "arm64" {
		expectedArch = "aarch64"
	}
	for tag, want := range map[int]string{rpmutils.NAME: "trace-dock", rpmutils.VERSION: "1.2.3", rpmutils.ARCH: expectedArch} {
		got, err := p.Header.GetString(tag)
		if err != nil || got != want {
			t.Fatalf("RPM identity %d: %q %v", tag, got, err)
		}
	}
	for _, tag := range []int{rpmutils.PREIN, rpmutils.POSTIN, rpmutils.PREUN, rpmutils.POSTUN} {
		if script, err := p.Header.GetString(tag); err == nil && script != "" {
			t.Fatal("RPM must not contain install/uninstall scripts")
		}
	}
	reader, err := p.PayloadReaderExtended()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Mode()&0170000 != 0100000 {
			continue
		}
		count++
		data, err := io.ReadAll(reader)
		if err != nil || strings.TrimPrefix(path.Clean(h.Name()), "/") != "usr/bin/trace-dock" || h.Mode()&0777 != 0755 || !bytes.Equal(data, binary) {
			t.Fatalf("unexpected RPM payload: %s %v", h.Name(), err)
		}
	}
	if count != 1 {
		t.Fatalf("RPM CLI count=%d", count)
	}
}

func verifyMacPkg(t *testing.T, name, arch string, binary []byte) {
	t.Helper()
	p, err := flatpkg.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	expectedArch := "x86_64"
	if arch == "arm64" {
		expectedArch = "arm64"
	}
	if p.Kind != flatpkg.KindProduct || len(p.Components) != 1 || p.Distribution.Options.HostArchitectures != expectedArch {
		t.Fatal("incorrect macOS product identity or architecture")
	}
	c := p.Components[0]
	if c.Info.Identifier != "org.trace-dock.cli" || c.Info.Version != "1.2.3" || c.Info.InstallLocation != "/" || c.Info.Scripts != nil {
		t.Fatal("incorrect macOS component or unexpected scripts")
	}
	reader, _, closer, err := c.OpenPayloadCPIO()
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	count := 0
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !h.IsRegular() {
			continue
		}
		count++
		data, err := io.ReadAll(reader)
		if err != nil || h.Name != "./usr/local/bin/trace-dock" || h.Mode&0777 != 0755 || !bytes.Equal(data, binary) {
			t.Fatalf("pkg installs unexpected file: %s %v", h.Name, err)
		}
	}
	if count != 1 {
		t.Fatalf("pkg CLI count=%d", count)
	}
}
