package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func npmFixture(t *testing.T, dir string) {
	t.Helper()
	for _, target := range targets {
		name := "trace-dock-" + target.GOOS + "-" + target.GOARCH + target.Suffix
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+" binary\x00"), 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func readNPMPackage(t *testing.T, file string) map[string][]byte {
	t.Helper()
	f, err := os.Open(file)
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
	contents := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(h.Name, "package/") || h.Typeflag != tar.TypeReg {
			t.Fatalf("unsafe npm archive entry: %+v", h)
		}
		if _, ok := contents[h.Name]; ok {
			t.Fatal("duplicate package entry")
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		contents[h.Name] = data
		if strings.HasPrefix(h.Name, "package/bin/") && h.Mode != 0755 {
			t.Fatal("npm binary/launcher must be executable")
		}
	}
	return contents
}

func TestNPMArchivePlatformSelectionAndIntegrity(t *testing.T) {
	dir := t.TempDir()
	npmFixture(t, dir)
	cfg := releaseConfig{Version: "1.2.3", NPM: npmConfig{Name: "@example/trace-dock"}}
	artifacts, err := buildNPMPackages(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 8 {
		t.Fatalf("unexpected npm files: %v", artifacts)
	}
	b, err := os.ReadFile(filepath.Join(dir, "npm", "publish.json"))
	if err != nil {
		t.Fatal(err)
	}
	var plan npmPublishPlan
	if err = json.Unmarshal(b, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Packages) != 6 || plan.Packages[5].Name != cfg.NPM.Name {
		t.Fatal("native packages must publish before entry package")
	}
	main := readNPMPackage(t, filepath.Join(dir, "npm", "trace-dock-cli.tgz"))
	if len(main) != 4 {
		t.Fatal("unexpected files in entry package")
	}
	var meta map[string]any
	if err = json.Unmarshal(main["package/package.json"], &meta); err != nil {
		t.Fatal(err)
	}
	if meta["name"] != cfg.NPM.Name || meta["version"] != cfg.Version || meta["scripts"] != nil {
		t.Fatal("entry package identity or lifecycle scripts")
	}
	deps := meta["optionalDependencies"].(map[string]any)
	if len(deps) != len(targets) {
		t.Fatal("optional native dependencies missing")
	}
	for i, target := range targets {
		pkg := plan.Packages[i]
		file := filepath.Join(dir, "npm", pkg.File)
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha512.Sum512(data)
		if pkg.Integrity != "sha512-"+base64.StdEncoding.EncodeToString(sum[:]) {
			t.Fatal("wrong publication integrity")
		}
		contents := readNPMPackage(t, file)
		if len(contents) != 2 {
			t.Fatal("native package contains unexpected data")
		}
		var native map[string]any
		if err = json.Unmarshal(contents["package/package.json"], &native); err != nil {
			t.Fatal(err)
		}
		osName, arch := npmTarget(target)
		if native["name"] != cfg.NPM.Name+"-"+osName+"-"+arch || native["os"].([]any)[0] != osName || native["cpu"].([]any)[0] != arch || deps[pkg.Name] != cfg.Version || native["scripts"] != nil {
			t.Fatalf("wrong native selector: %v", native)
		}
		original, err := os.ReadFile(filepath.Join(dir, "trace-dock-"+target.GOOS+"-"+target.GOARCH+target.Suffix))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original, contents["package/bin/trace-dock"+target.Suffix]) {
			t.Fatal("npm binary bytes changed")
		}
	}
}

func TestNPMConfigValidation(t *testing.T) {
	for _, name := range []string{"@bad/name;command", "UPPERCASE", "../trace-dock", "@scope/", "@a/name space"} {
		c := npmConfig{Name: name}.defaults()
		if err := c.validate(); err == nil {
			t.Fatalf("invalid name accepted: %s", name)
		}
	}
	for _, registry := range []string{"http://remote.example", "https://user:secret@registry.npmjs.org/", "https://registry.npmjs.org/?secret=x"} {
		c := npmConfig{Registry: registry}.defaults()
		if err := c.validate(); err == nil {
			t.Fatalf("invalid registry: %s", registry)
		}
	}
	if err := (npmConfig{}).defaults().validate(); err != nil {
		t.Fatal(err)
	}
}

// Opt-in because ordinary CLI development only needs Go. This uses a local
// registry and temporary npm prefixes/caches; it never publishes to npmjs.org.
func TestNPMInstall(t *testing.T) {
	if os.Getenv("TRACE_DOCK_TEST_NPM") != "1" {
		t.Skip("run make test-npm for real npm installation checks")
	}
	for _, command := range []string{"node", "npm"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatal(command + " is required for npm integration testing")
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("local registry publication test runs on Linux/macOS; packages support Windows installs")
	}
	workspace := t.TempDir()
	output := filepath.Join(workspace, "dist")
	if err := os.MkdirAll(output, 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(workspace, "main.go")
	program := `package main
import("os";"fmt";"encoding/json")
func main(){if len(os.Args)>1&&os.Args[1]=="--version"{fmt.Println("1.2.3");return};if len(os.Args)>1&&os.Args[1]=="--exit-seven"{os.Exit(7)};json.NewEncoder(os.Stdout).Encode(os.Args[1:])}`
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	native := filepath.Join(workspace, "native-fixture")
	build := exec.Command(goExe, "build", "-o", native, source)
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOTOOLCHAIN=local")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile fixture: %v\n%s", err, data)
	}
	currentOS, currentArch := npmTarget(target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	for _, target := range targets {
		name := "trace-dock-" + target.GOOS + "-" + target.GOARCH + target.Suffix
		if target.GOOS == runtime.GOOS && target.GOARCH == runtime.GOARCH {
			if err := os.Rename(native, filepath.Join(output, name)); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(filepath.Join(output, name), []byte(name+" unsupported fixture"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := releaseConfig{Version: "1.2.3", NPM: npmConfig{Name: "@trace-dock-test/cli"}}
	if _, err := buildNPMPackages(output, cfg); err != nil {
		t.Fatal(err)
	}
	var plan npmPublishPlan
	data, err := os.ReadFile(filepath.Join(output, "npm", "publish.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	manifests := map[string]map[string]any{}
	packages := map[string]npmPublishPackage{}
	downloads := map[string]int{}
	var mu sync.Mutex
	for _, pkg := range plan.Packages {
		content := readNPMPackage(t, filepath.Join(output, "npm", pkg.File))
		var m map[string]any
		if err = json.Unmarshal(content["package/package.json"], &m); err != nil {
			t.Fatal(err)
		}
		manifests[pkg.Name] = m
		packages[pkg.Name] = pkg
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/tarballs/") {
			file := strings.TrimPrefix(r.URL.Path, "/tarballs/")
			mu.Lock()
			downloads[file]++
			mu.Unlock()
			http.ServeFile(w, r, filepath.Join(output, "npm", filepath.Base(file)))
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		m, ok := manifests[name]
		if !ok {
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]any{"error": "not_found"})
			return
		}
		copy := map[string]any{}
		for k, v := range m {
			copy[k] = v
		}
		pkg := packages[name]
		copy["dist"] = map[string]any{"tarball": server.URL + "/tarballs/" + pkg.File, "integrity": pkg.Integrity}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"name": name, "dist-tags": map[string]string{"latest": cfg.Version}, "versions": map[string]any{cfg.Version: copy}})
	}))
	defer server.Close()
	prefix := filepath.Join(workspace, "prefix")
	cache := filepath.Join(workspace, "cache")
	userconfig := filepath.Join(workspace, "npmrc")
	if err = os.WriteFile(userconfig, nil, 0600); err != nil {
		t.Fatal(err)
	}
	dataHome := filepath.Join(workspace, "user-data")
	if err = os.MkdirAll(dataHome, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(dataHome, "config.json")
	if err = os.WriteFile(sentinel, []byte("keep user data"), 0600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "NPM_CONFIG_USERCONFIG="+userconfig, "NPM_CONFIG_CACHE="+cache, "NPM_CONFIG_REGISTRY="+server.URL, "TRACE_DOCK_HOME="+dataHome)
	npm := func(args ...string) {
		t.Helper()
		cmd := exec.Command("npm", args...)
		cmd.Env = env
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("npm %v: %v\n%s", args, err, data)
		}
	}
	npm("install", "-g", cfg.NPM.Name+"@"+cfg.Version, "--prefix", prefix, "--registry", server.URL, "--ignore-scripts", "--no-audit", "--no-fund")
	cli := filepath.Join(prefix, "bin", "trace-dock")
	cmd := exec.Command(cli, "--version")
	cmd.Env = env
	if data, err := cmd.CombinedOutput(); err != nil || strings.TrimSpace(string(data)) != cfg.Version {
		t.Fatalf("installed command: %v %s", err, data)
	}
	cmd = exec.Command(cli, "--exit-seven")
	cmd.Env = env
	if err := cmd.Run(); err == nil || cmd.ProcessState.ExitCode() != 7 {
		t.Fatal("launcher lost exit code")
	}
	cmd = exec.Command(cli, "a path with spaces", "; echo never-executed")
	cmd.Env = env
	data, err = cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var forwarded []string
	if err = json.Unmarshal(data, &forwarded); err != nil || len(forwarded) != 2 || forwarded[1] != "; echo never-executed" {
		t.Fatal("arguments were changed or interpreted by shell")
	}
	osName, arch := currentOS, currentArch
	wantFile := "trace-dock-" + osName + "-" + arch + ".tgz"
	mu.Lock()
	for _, pkg := range plan.Packages {
		if pkg.File == "trace-dock-cli.tgz" || pkg.File == wantFile {
			if downloads[pkg.File] < 1 {
				t.Errorf("did not download %s", pkg.File)
			}
		} else if downloads[pkg.File] != 0 {
			t.Errorf("downloaded non-host platform: %s", pkg.File)
		}
	}
	mu.Unlock()
	// Tampering must be rejected by the entry point before execution.
	nativeName := cfg.NPM.Name + "-" + osName + "-" + arch
	installed := filepath.Join(prefix, "lib", "node_modules", filepath.FromSlash(cfg.NPM.Name), "node_modules", filepath.FromSlash(nativeName), "bin", "trace-dock")
	if err = os.WriteFile(installed, []byte("tampered"), 0755); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(cli, "--version")
	cmd.Env = env
	if data, err = cmd.CombinedOutput(); err == nil || !strings.Contains(string(data), "checksum mismatch") {
		t.Fatalf("tampered executable accepted: %v %s", err, data)
	}
	npm("uninstall", "-g", cfg.NPM.Name, "--prefix", prefix, "--ignore-scripts", "--no-audit", "--no-fund")
	if _, err = os.Lstat(cli); !os.IsNotExist(err) {
		t.Fatal("npm command remains after uninstall")
	}
	data, err = os.ReadFile(sentinel)
	if err != nil || string(data) != "keep user data" {
		t.Fatal("npm uninstall changed user data")
	}
	fmt.Println("PASS: npm installs only host binary, exposes command, preserves arguments/exit codes, rejects tampering and uninstalls without touching user data")
}
