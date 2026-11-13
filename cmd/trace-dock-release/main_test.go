package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestGitHubReleaseWorkflow(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow map[string]any
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("invalid GitHub Actions YAML: %v", err)
	}
	permissions := asYAMLMap(workflow["permissions"])
	if permissions["contents"] != "write" {
		t.Fatal("release workflow needs contents: write")
	}
	on := asYAMLMap(workflow["on"])
	push := asYAMLMap(on["push"])
	tags, ok := push["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != "v*" {
		t.Fatalf("release must trigger on version tags: %#v", push)
	}
	jobs := asYAMLMap(workflow["jobs"])
	build, publish := asYAMLMap(jobs["build"]), asYAMLMap(jobs["publish"])
	if publish["needs"] != "build" {
		t.Fatal("publishing must wait for build and verification")
	}
	buildSteps, publishSteps := asYAMLMaps(build["steps"]), asYAMLMaps(publish["steps"])
	if !hasRunCommand(buildSteps, "go test ./...") || !hasRunCommand(buildSteps, "go vet ./...") || !hasRunCommand(buildSteps, "go run ./cmd/trace-dock-release --ci") {
		t.Fatal("build job is missing test/release steps")
	}
	if !hasSecretEnvironment(publishSteps, "NPM_TOKEN", "secrets.NPM_TOKEN") || !hasRunCommand(publishSteps, "node dist/npm/publish.cjs") {
		t.Fatal("publish job must use the npm token secret and publisher")
	}
	if !hasRunCommand(publishSteps, "gh release create") {
		t.Fatal("publish job must create a GitHub release")
	}
}

func asYAMLMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func asYAMLMaps(v any) []map[string]any {
	xs, _ := v.([]any)
	out := make([]map[string]any, 0, len(xs))
	for _, x := range xs {
		out = append(out, asYAMLMap(x))
	}
	return out
}
func hasRunCommand(steps []map[string]any, needle string) bool {
	for _, s := range steps {
		if strings.Contains(fmt.Sprint(s["run"]), needle) {
			return true
		}
	}
	return false
}
func hasSecretEnvironment(steps []map[string]any, key, needle string) bool {
	for _, s := range steps {
		env := asYAMLMap(s["env"])
		if strings.Contains(fmt.Sprint(env[key]), needle) {
			return true
		}
	}
	return false
}
