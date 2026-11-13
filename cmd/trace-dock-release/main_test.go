package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseArchivesPreserveBinaryAndMode(t *testing.T) {
	for _, target := range targets {
		t.Run(target.GOOS+"/"+target.GOARCH, func(t *testing.T) {
			dir := t.TempDir()
			name := "trace-dock-" + target.GOOS + "-" + target.GOARCH + target.Suffix
			content := []byte("a compiled binary fixture\x00\xff")
			if err := os.WriteFile(filepath.Join(dir, name), content, 0755); err != nil {
				t.Fatal(err)
			}
			if err := archiveBinary(dir, name, target); err != nil {
				t.Fatal(err)
			}
			archivePath := filepath.Join(dir, archiveName(target))
			if target.GOOS == "windows" {
				z, err := zip.OpenReader(archivePath)
				if err != nil {
					t.Fatal(err)
				}
				defer z.Close()
				if len(z.File) != 1 || z.File[0].Name != "trace-dock.exe" {
					t.Fatalf("unexpected ZIP contents: %+v", z.File)
				}
				f, err := z.File[0].Open()
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				actual, err := io.ReadAll(f)
				if err != nil || !bytes.Equal(content, actual) {
					t.Fatalf("ZIP changed executable: %v", err)
				}
			} else {
				f, err := os.Open(archivePath)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				gz, err := gzip.NewReader(f)
				if err != nil {
					t.Fatal(err)
				}
				defer gz.Close()
				tr := tar.NewReader(gz)
				header, err := tr.Next()
				if err != nil {
					t.Fatal(err)
				}
				if header.Name != "trace-dock" || header.Mode != 0755 || header.Typeflag != tar.TypeReg {
					t.Fatalf("unexpected TAR header: %+v", header)
				}
				actual, err := io.ReadAll(tr)
				if err != nil || !bytes.Equal(content, actual) {
					t.Fatalf("TAR changed executable: %v", err)
				}
				if _, err := tr.Next(); err != io.EOF {
					t.Fatalf("unexpected archive member: %v", err)
				}
			}
			before, err := os.ReadFile(archivePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := archiveBinary(dir, name, target); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(archivePath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("archive is not deterministic: %v", err)
			}
		})
	}
}

func TestCIRequiresOneCanonicalVersionTag(t *testing.T) {
	for _, test := range []struct{ input, tag string }{
		{"v0.1.1\n", "v0.1.1"},
		{"unrelated\nv1.2.3\n", "v1.2.3"},
		{"", ""},
		{"v01.2.3", ""},
		{"v1.2.3-rc.1", ""},
		{"v1.2.3\nv1.2.4\n", ""},
	} {
		got, err := selectReleaseTag(test.input)
		if test.tag == "" {
			if err == nil {
				t.Fatalf("accepted ambiguous or invalid tag: %q", test.input)
			}
		} else if err != nil || got != test.tag {
			t.Fatalf("%q: %q, %v", test.input, got, err)
		}
	}
}

func TestReleaseVersionIncrementAndURLValidation(t *testing.T) {
	for input, want := range map[string]string{"0.1.0": "0.1.1", "1.2.99": "1.2.100"} {
		got, err := nextVersion(input)
		if err != nil || got != want {
			t.Fatalf("%s: %s %v", input, got, err)
		}
	}
	if _, err := nextVersion("1.2.18446744073709551615"); err == nil {
		t.Fatal("patch overflow accepted")
	}
	for _, server := range []string{"", "http://collector.example.com", "https://user:secret@collector.example.com", "https://collector.example.com?token=secret", "https://collector.example.com/ -X main.version=oops"} {
		if err := validateConfig(releaseConfig{Version: "1.2.3", DefaultServerURL: server}); err == nil {
			t.Fatalf("invalid release URL accepted: %s", server)
		}
	}
	if err := validateConfig(releaseConfig{Version: "1.2.3", DefaultServerURL: "https://collector.example.com"}); err != nil {
		t.Fatal(err)
	}
}
