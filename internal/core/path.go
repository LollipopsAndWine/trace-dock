package core

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var windowsPath = regexp.MustCompile(`^[a-zA-Z]:[\\/]`)

func Normalize(p string) string {
	if windowsPath.MatchString(p) || strings.HasPrefix(p, `\\`) {
		return strings.ToLower(path.Clean(strings.ReplaceAll(p, `\`, "/")))
	}
	return filepath.Clean(p)
}
func containsUnsafe(p string) bool {
	for _, v := range strings.Split(strings.ReplaceAll(p, `\`, "/"), "/") {
		if v == ".." || strings.ContainsRune(v, 0) {
			return true
		}
	}
	return strings.Contains(p, ":") || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)
}
func within(root, p string) bool {
	r, e := filepath.Rel(root, p)
	return e == nil && (r == "." || SafeRelative(r))
}
func metadata(p string) bool { _, e := os.Lstat(p); return e == nil }
func ownedPath(p, home string) bool {
	// Paths in another user's home require separate authorization, which MVP never assumes.
	norm := Normalize(p)
	hn := Normalize(home)
	for _, prefix := range []string{"/home/", "/Users/", "c:/users/"} {
		if strings.HasPrefix(norm, prefix) && !within(hn, norm) {
			return false
		}
	}
	return ownedByCurrentUser(p)
}
func Classify(raw, home string) Workspace {
	w := Workspace{Raw: raw, Normalized: Normalize(raw), Kind: "unknown", Reason: "no_project_evidence", Policy: "path_only"}
	w.Platform = runtime.GOOS
	if windowsPath.MatchString(raw) || strings.HasPrefix(raw, `\\`) {
		w.Platform = "windows"
	}
	if raw == "" || !filepath.IsAbs(raw) || (runtime.GOOS != "windows" && windowsPath.MatchString(raw)) {
		w.Reason = "unavailable_path_platform"
		return w
	}
	st, e := os.Stat(raw)
	if e != nil || !st.IsDir() {
		w.Kind = "missing"
		w.Reason = "directory_missing"
		return w
	}
	w.Exists = true
	mtime := st.ModTime().UTC()
	w.DirectoryMTime = &mtime
	real, e := filepath.EvalSymlinks(raw)
	if e != nil {
		w.Reason = "unresolved_path"
		return w
	}
	realHome, _ := filepath.EvalSymlinks(home)
	if Normalize(real) == Normalize(realHome) {
		w.Kind = "home"
		w.Reason = "user_home"
		return w
	}
	if !ownedPath(real, home) {
		w.CrossOwner = true
		w.Reason = "cross_owner_requires_authorization"
		return w
	}
	// Agent state and our own job directory must never become a workspace snapshot.
	for _, p := range []string{filepath.Join(home, ".codex"), filepath.Join(home, ".claude"), filepath.Join(home, ".trace-dock"), os.Getenv("TRACE_DOCK_HOME")} {
		if p != "" && within(real, p) {
			w.Reason = "contains_private_agent_or_job_state"
			return w
		}
		if p != "" && within(p, real) {
			w.Reason = "private_agent_or_job_state"
			return w
		}
	}
	base := strings.ToLower(filepath.Base(real))
	for _, n := range []string{"checkpoints", "checkpoint", "datasets", "dataset", "training_logs", "artifacts", "runs"} {
		if base == n {
			w.Kind = "artifact"
			w.Reason = "artifact_directory"
			return w
		}
	}
	// An anchored project may contain submodules; do not classify it as a parent.
	project := false
	for _, n := range []string{".git", "go.mod", "pyproject.toml", "package.json", "Cargo.toml", "pom.xml", "CMakeLists.txt", "setup.py", "Makefile"} {
		if metadata(filepath.Join(real, n)) {
			project = true
			break
		}
	}
	entries, e := os.ReadDir(real)
	if e != nil {
		w.Reason = "directory_unreadable"
		return w
	}
	repos := 0
	for _, ent := range entries {
		if ent.IsDir() && metadata(filepath.Join(real, ent.Name(), ".git")) {
			repos++
		}
	}
	if !project && repos >= 2 {
		w.Kind = "parent"
		w.Reason = "multiple_independent_projects"
		return w
	}
	if project {
		w.Kind = "project"
		w.Policy = "snapshot"
		w.Reason = "project_marker"
	}
	return w
}
