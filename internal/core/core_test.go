package core

import (
	"archive/tar"
	"context"
	"encoding/json"
	"github.com/klauspost/compress/zstd"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, p, b string) {
	t.Helper()
	if e := WritePrivate(p, []byte(b)); e != nil {
		t.Fatal(e)
	}
}
func codexLog(cwd, kind string) string {
	a, _ := json.Marshal(map[string]any{"type": "session_meta", "timestamp": "2026-01-01T00:00:00Z", "payload": map[string]any{"cwd": cwd}})
	return string(a) + "\n" + kind + "\n" + `{"type":"event_msg","timestamp":"2026-01-01T00:01:00Z","payload":{"type":"task_complete"}}` + "\n"
}
func TestCodexSchemasAndDamage(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	project := filepath.Join(home, "code")
	fixture(t, filepath.Join(project, "go.mod"), "module example\n")
	variants := []struct{ id, events, schema, skip string }{
		{"11111111-1111-1111-1111-111111111111", `{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{}"}}`, "codex.response_item.v1", ""},
		{"22222222-2222-2222-2222-222222222222", `{"ordinal":3,"thread_name":"main","payload":{"type":"custom_tool_call","name":"exec"}}`, "codex.payload.v2", ""},
		{"33333333-3333-3333-3333-333333333333", `{"type":"other"}`, "unsupported", "unsupported_codex_schema"},
		{"44444444-4444-4444-4444-444444444444", "{\"type\":\"response_item\"}\n{\"payload\":{\"type\":\"item_completed\"}}", "unsupported", "mixed_codex_schema"},
		{"55555555-5555-5555-5555-555555555555", "{\"type\":\"response_item\"}\ninvalid-json", "codex.response_item.v1", ""},
	}
	for _, v := range variants {
		fixture(t, filepath.Join(root, "2026", "01", "01", "rollout-time-"+v.id+".jsonl"), codexLog(project, v.events))
	}
	fixture(t, filepath.Join(root, "rollout-66666666-6666-6666-6666-666666666666.jsonl"), codexLog(project, `{"type":"response_item"}`))
	sessions, e := Discover("codex", home, root)
	if e != nil || len(sessions) != 5 {
		t.Fatalf("%d %v", len(sessions), e)
	}
	for _, s := range sessions {
		for _, v := range variants {
			if s.ID == v.id {
				if s.Schema != v.schema || s.SkipReason != v.skip {
					t.Fatalf("%s: %+v", v.id, s)
				}
				if strings.HasPrefix(s.ID, "555") && s.Validity != "invalid" {
					t.Fatal("damaged log valid")
				}
			}
		}
		if s.Reversibility == "reconstructable" {
			t.Fatal("unverified reconstruction")
		}
	}
}

func TestClaudeChildrenDedupAndWindows(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".claude", "projects", "---")
	project := filepath.Join(home, "project")
	fixture(t, filepath.Join(project, "package.json"), "{}")
	log := func(extra string) string {
		a, _ := json.Marshal(map[string]any{"type": "user", "cwd": project, "timestamp": "2026-01-01T00:00:00Z"})
		return string(a) + "\n" + extra + `{"type":"assistant","message":{"stop_reason":"end_turn","content":[]},"timestamp":"2026-01-01T00:01:00Z"}` + "\n"
	}
	fixture(t, filepath.Join(root, "root.jsonl"), log(""))
	fixture(t, filepath.Join(root, "root", "subagents", "agent-a.jsonl"), log(""))
	fixture(t, filepath.Join(root, "deeper", "fork.jsonl"), log("{\"type\":\"system\",\"forkedFromSessionId\":\"root\"}\n"))
	sessions, e := Discover("claude", home, filepath.Dir(root))
	if e != nil || len(sessions) != 1 {
		t.Fatalf("%v %+v", e, sessions)
	}
	s := sessions[0]
	if s.ID != "fork" || s.RootID != "root" || len(s.Aliases) != 1 || len(s.Children) != 1 || s.Workspace.Raw != project {
		t.Fatalf("%+v", s)
	}
	if Normalize(`C:\Users\Name\Repo`) != Normalize(`c:\users\name\repo`) {
		t.Fatal("Windows path comparison")
	}
	if Normalize("/tmp/A") == Normalize("/tmp/a") {
		t.Fatal("Unix case folded")
	}
}

func TestClassificationAndRules(t *testing.T) {
	home := t.TempDir()
	parent := filepath.Join(home, "repos")
	fixture(t, filepath.Join(parent, "a", ".git", "HEAD"), "ref: main")
	fixture(t, filepath.Join(parent, "b", ".git", "HEAD"), "ref: main")
	if w := Classify(home, home); w.Kind != "home" {
		t.Fatal(w)
	}
	if w := Classify(parent, home); w.Kind != "parent" {
		t.Fatal(w)
	}
	fixture(t, filepath.Join(parent, "go.mod"), "module parent\n")
	if w := Classify(parent, home); w.Kind != "project" {
		t.Fatal(w)
	}
	r := Rules{LargeFileThreshold: 10, Whitelist: []string{"*.analysis.json"}, Blacklist: []string{"*.pt", "datasets/**"}}
	for _, v := range []struct {
		p     string
		n     int64
		large bool
	}{{".git/objects/pack/a.pack", 100, false}, {"result.analysis.json", 100, false}, {"model.pt", 1, true}, {"datasets/a.txt", 1, true}, {"big.txt", 11, true}, {"small.txt", 9, false}} {
		if IsLarge(v.p, v.n, r) != v.large {
			t.Fatal(v)
		}
	}
}

func TestCollectionSafetyAndDeterminism(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "selected")
	unselected := filepath.Join(home, "unselected")
	fixture(t, filepath.Join(project, "go.mod"), "module example\n")
	fixture(t, filepath.Join(project, "main.go"), "package main\n")
	fixture(t, filepath.Join(project, ".env"), "password=very_sensitive_value\n")
	fixture(t, filepath.Join(project, "big.pt"), "weights")
	fixture(t, filepath.Join(project, ".git", "objects", "pack", "x.pack"), strings.Repeat("safe", 20))
	fixture(t, filepath.Join(unselected, "private"), "must never be collected")
	fixture(t, filepath.Join(project, ".claude", ".credentials.json"), "private")
	// Windows may require elevated privileges for symlinks; the remaining safety checks still run.
	_ = os.Symlink(unselected, filepath.Join(project, "escape"))
	src := filepath.Join(home, "trace.jsonl")
	fixture(t, src, `{"type":"user","cwd":`+quoteJSON(project)+`,"timestamp":"2026-01-01T00:00:00Z"}`+"\n"+`{"type":"assistant","message":{"stop_reason":"end_turn","content":[]},"timestamp":"2026-01-01T00:01:00Z"}`+"\n")
	rules := Rules{LargeFileThreshold: 1024, Blacklist: []string{"*.pt"}}
	scanner, e := NewScanner(rules, "participant-token-12345678")
	if e != nil {
		t.Fatal(e)
	}
	var hashes []string
	for i := 0; i < 2; i++ {
		dir := t.TempDir()
		s := parseSession("claude", src, 1, home)
		s.Schema = "claude.v1"
		s.Eligibility = "archive_only"
		s.Workspace = Classify(project, home)
		s.ID = "root"
		m := &Manifest{JobID: "job", Tool: "claude", ConsentedAt: time.Now(), Sessions: []*Session{s}, Selection: []string{"root"}, Rules: rules}
		c := Collector{Dir: dir, Home: home, Scanner: scanner}
		if e = c.Collect(context.Background(), m); e != nil {
			t.Fatal(e)
		}
		s = m.Sessions[0]
		if m.Summary.Excluded < 3 || m.Summary.Workspaces != 1 || m.Summary.LargeFiles != 1 || s.Delay == nil {
			t.Fatalf("%+v", m.Summary)
		}
		for _, a := range m.Artifacts {
			if a.Kind != "workspace" {
				continue
			}
			hashes = append(hashes, a.SHA256)
			f, e := os.Open(a.LocalPath)
			if e != nil {
				t.Fatal(e)
			}
			z, e := zstd.NewReader(f)
			if e != nil {
				t.Fatal(e)
			}
			tr := tar.NewReader(z)
			names := map[string]bool{}
			for {
				h, e := tr.Next()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				names[h.Name] = true
				if strings.Contains(h.Name, ".env") || strings.Contains(h.Name, "escape") || strings.Contains(h.Name, ".claude") || strings.Contains(h.Name, "big.pt") {
					t.Fatal("unsafe archive entry", h.Name)
				}
			}
			z.Close()
			f.Close()
			if !names[".git/objects/pack/x.pack"] || !names["main.go"] {
				t.Fatal(names)
			}
		}
		data, e := os.ReadFile(filepath.Join(dir, "large-files", s.WorkspaceArtifact+".jsonl"))
		if e != nil {
			t.Fatal(e)
		}
		var l map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(string(data))), &l) != nil || len(l) != 2 {
			t.Fatal(string(data))
		}
	}
	if hashes[0] != hashes[1] {
		t.Fatal("non-deterministic archive")
	}
	data, _ := os.ReadFile(filepath.Join(project, ".env"))
	if string(data) != "password=very_sensitive_value\n" {
		t.Fatal("source modified")
	}
}

func quoteJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestScannerBoundaryAndFailureClosed(t *testing.T) {
	s, _ := NewScanner(Rules{}, "exact-token-value")
	payload := strings.Repeat("x", 65530) + "-----BEGIN OPENSSH PRIVATE KEY-----"
	hit, e := s.Copy(io.Discard, strings.NewReader(payload))
	if e != nil || !hit {
		t.Fatal(hit, e)
	}
	if _, e = NewScanner(Rules{SecretPatterns: []string{"["}}, ""); e == nil {
		t.Fatal("bad scanner rules accepted")
	}
	if !s.Hit([]byte("exact-token-value")) {
		t.Fatal("participant token not detected")
	}
}

func TestDownloadEvidenceAndSelectedScopeRefresh(t *testing.T) {
	downloads := map[string]string{}
	extractDownloads(map[string]any{"command": `curl -L https://example.com/model.pt -o model.pt`}, downloads)
	extractDownloads(map[string]any{"command": `wget https://example.com/data.bin -O data.bin`}, downloads)
	extractDownloads(map[string]any{"command": `aws s3 cp s3://bucket/dataset.parquet dataset.parquet`}, downloads)
	if downloads["model.pt"] != "https://example.com/model.pt" || downloads["data.bin"] != "https://example.com/data.bin" || downloads["dataset.parquet"] != "s3://bucket/dataset.parquet" {
		t.Fatal(downloads)
	}
	home := t.TempDir()
	src := filepath.Join(home, "root.jsonl")
	log := func(cwd string) string {
		return `{"type":"user","cwd":` + quoteJSON(cwd) + `}` + "\n" + `{"type":"assistant","message":{"stop_reason":"end_turn"}}` + "\n"
	}
	fixture(t, src, log(filepath.Join(home, "before")))
	old := parseSession("claude", src, 1, home)
	fixture(t, src, log(filepath.Join(home, "after")))
	refreshed := RefreshSelected([]*Session{old}, home)
	if len(refreshed) != 1 || refreshed[0].Eligibility != "excluded" || refreshed[0].SkipReason != "cwd_changed_after_selection" || refreshed[0].Workspace.Raw != old.Workspace.Raw {
		t.Fatal("selection scope expanded on refresh")
	}
}
