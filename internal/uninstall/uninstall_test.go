package uninstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNPMManagedUninstallPreservesData(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "config.json")
	if err := os.WriteFile(sentinel, []byte("user data"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TRACE_DOCK_HOME", dir)
	t.Setenv("TRACE_DOCK_NPM_PACKAGE", "@example/trace-dock")
	err := Run(nil)
	if err == nil || !strings.Contains(err.Error(), "npm uninstall -g @example/trace-dock") {
		t.Fatalf("npm uninstall guard: %v", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "user data" {
		t.Fatal("npm-managed uninstall changed user data")
	}
}

func TestExecutableSafetyChecks(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "trace-dock")
	if err := os.WriteFile(good, []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(good)
	if err != nil || !st.Mode().IsRegular() {
		t.Fatal("regular CLI fixture rejected")
	}
	link := filepath.Join(dir, "trace-dock-link")
	if err := os.Symlink(good, link); err == nil {
		linked, err := os.Lstat(link)
		if err != nil || linked.Mode()&os.ModeSymlink == 0 {
			t.Fatal("symlink fixture invalid")
		}
	}
	if filepath.Base(good) != "trace-dock" {
		t.Fatal("fixture name")
	}
}

func TestDataHomeRefusesUnknownFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "trace-dock")
	if err := os.MkdirAll(filepath.Join(dir, "jobs", "job"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "jobs", "job", "job.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateDataHome(dir); err != nil {
		t.Fatal("known CLI data rejected:", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "user-not-cli-data.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateDataHome(dir); err == nil {
		t.Fatal("unknown user file was accepted for deletion")
	}
}

func TestDataHomeAcceptsInterruptedWorkspaceStaging(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "trace-dock")
	job := filepath.Join(dir, "jobs", "job")
	temp := filepath.Join(job, "workspace-staging-1482074104")
	if err := os.MkdirAll(filepath.Join(temp, "repo", "src"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{
		filepath.Join(job, "job.json"),
		filepath.Join(temp, "repo", "go.mod"),
		filepath.Join(temp, "repo", "src", "main.go"),
	} {
		if err := os.WriteFile(file, []byte("staging"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateDataHome(dir); err != nil {
		t.Fatalf("interrupted CLI workspace staging should be recognized: %v", err)
	}
}

func TestManagedDataStartupDoesNotFailOnExistingJobFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "trace-dock")
	if err := os.MkdirAll(filepath.Join(dir, "jobs", "job", "workspace-staging-old"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "jobs", "job", "job.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "jobs", "job", "workspace-staging-old", "partial-file"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings-from-newer-cli.json"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := MarkManagedData(dir); err != nil {
		t.Fatalf("CLI startup marker should tolerate existing data: %v", err)
	}
	if err := validateDataHome(dir); err == nil {
		t.Fatal("uninstall validation should still reject unknown files")
	}
	if _, err := os.Stat(filepath.Join(dir, "jobs", "job", "workspace-staging-old", "partial-file")); err != nil {
		t.Fatal("startup validation modified interrupted staging data")
	}
}

func TestDataHomeRejectsSymlinkInsideWorkspaceStaging(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "trace-dock")
	temp := filepath.Join(dir, "jobs", "job", "workspace-staging-abandoned")
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "jobs", "job", "job.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "user-file")
	if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(temp, "external-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := validateDataHome(dir); err == nil {
		t.Fatal("workspace staging symlink was accepted")
	}
}
