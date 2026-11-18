package core

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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
		{"33333333-3333-3333-3333-333333333333", `{"type":"other"}`, "unknown", ""},
		{"44444444-4444-4444-4444-444444444444", "{\"type\":\"response_item\"}\n{\"payload\":{\"type\":\"item_completed\"}}", "codex.mixed", ""},
		{"55555555-5555-5555-5555-555555555555", "{\"type\":\"response_item\"}\ninvalid-json", "codex.response_item.v1", ""},
		{"66666666-6666-6666-6666-666666666666", "{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\"}}\n{\"type\":\"response_item\",\"payload\":{\"type\":\"custom_tool_call\",\"name\":\"exec\"}}\n{\"type\":\"event_msg\",\"payload\":{\"type\":\"item_completed\"}}", "codex.payload.v2", ""},
		{"77777777-7777-7777-7777-777777777777", `{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec"}}`, "codex.payload.v2", ""},
		{"88888888-8888-8888-8888-888888888888", "{\"type\":\"response_item\",\"payload\":{\"type\":\"message\"}}\n{\"type\":\"event_msg\",\"payload\":{\"type\":\"item_completed\"}}", "codex.payload.v2", ""},
		{"99999999-9999-9999-9999-999999999999", `{"type":"unknown_wrapper","payload":{"type":"custom_tool_call","name":"exec"}}`, "unknown", ""},
	}
	for _, v := range variants {
		fixture(t, filepath.Join(root, "2026", "01", "01", "rollout-time-"+v.id+".jsonl"), codexLog(project, v.events))
	}
	fixture(t, filepath.Join(root, "rollout-66666666-6666-6666-6666-666666666666.jsonl"), codexLog(project, `{"type":"response_item"}`))
	sessions, e := Discover("codex", home, root)
	if e != nil || len(sessions) != len(variants) {
		t.Fatalf("%d %v", len(sessions), e)
	}
	for _, s := range sessions {
		for _, v := range variants {
			if s.ID == v.id {
				if s.Schema != v.schema || s.SkipReason != v.skip || s.Eligibility == "excluded" {
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

func TestCodexSubagentsLinkFromSessionMetadataAndArchivedRoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	project := filepath.Join(home, "code")
	const rootID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const childID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const grandchildID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	const legacyID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	const orphanID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	const unlinkedID = "99999999-9999-4999-8999-999999999999"

	writeSession := func(dir, id string, metadata map[string]any) {
		t.Helper()
		metadata["id"] = id
		metadata["cwd"] = project
		meta, err := json.Marshal(map[string]any{"type": "session_meta", "payload": metadata})
		if err != nil {
			t.Fatal(err)
		}
		content := string(meta) + "\n" + `{"type":"event_msg","payload":{"type":"task_complete"}}` + "\n"
		fixture(t, filepath.Join(dir, "rollout-"+id+".jsonl"), content)
	}
	spawnSource := func(parent string) map[string]any {
		return map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{
			"parent_thread_id": parent,
			"depth":            1,
		}}}
	}

	writeSession(filepath.Join(root, "2026", "11", "15"), rootID, map[string]any{"source": "cli"})
	writeSession(filepath.Join(root, "2026", "11", "15"), childID, map[string]any{"source": spawnSource(rootID)})
	writeSession(filepath.Join(root, "2026", "11", "15"), grandchildID, map[string]any{"source": spawnSource(childID)})
	writeSession(filepath.Join(home, ".codex", "archived_sessions", "2026", "11", "15"), legacyID, map[string]any{"parent_thread_id": rootID})
	writeSession(filepath.Join(root, "2026", "11", "15"), orphanID, map[string]any{"source": spawnSource("ffffffff-ffff-4fff-8fff-ffffffffffff")})
	writeSession(filepath.Join(root, "2026", "11", "15"), unlinkedID, map[string]any{"thread_source": "subagent"})

	sessions, err := Discover("codex", home, root)
	if err != nil {
		t.Fatal(err)
	}
	var rootSession, orphan, unlinked *Session
	for _, session := range sessions {
		switch session.ID {
		case rootID:
			rootSession = session
		case orphanID:
			orphan = session
		case unlinkedID:
			unlinked = session
		}
	}
	if rootSession == nil || rootSession.SubagentCount() != 3 {
		t.Fatalf("root should contain nested and archived subagents: %+v", rootSession)
	}
	if len(rootSession.Children) != 2 || rootSession.Children[0].ID == grandchildID {
		t.Fatalf("root should attach its direct child threads: %+v", rootSession.Children)
	}
	if orphan == nil || orphan.Eligibility != "excluded" || orphan.SkipReason != "orphan_or_cyclic_subagent" {
		t.Fatalf("orphan child should not become an independently selectable root: %+v", orphan)
	}
	if unlinked == nil || unlinked.Eligibility != "excluded" || unlinked.SkipReason != "subagent_parent_unavailable" {
		t.Fatalf("subagent missing its parent id should not become an independently selectable root: %+v", unlinked)
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
	fixture(t, filepath.Join(root, "explicit-agent.jsonl"), log("{\"type\":\"system\",\"parentSessionId\":\"root\"}\n"))
	sessions, e := Discover("claude", home, filepath.Dir(root))
	if e != nil || len(sessions) != 1 {
		t.Fatalf("%v %+v", e, sessions)
	}
	s := sessions[0]
	if s.ID != "fork" || s.RootID != "root" || len(s.Aliases) != 1 || len(s.Children) != 2 || s.Workspace.Raw != project {
		t.Fatalf("%+v", s)
	}
	childIDs := map[string]bool{}
	for _, child := range s.Children {
		childIDs[child.ID] = true
	}
	if !childIDs["agent-a"] || !childIDs["explicit-agent"] {
		t.Fatalf("Claude child transcripts were not associated by directory and explicit parent ID: %+v", s.Children)
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
	fixture(t, filepath.Join(project, ".env"), "api_key=ghp_"+strings.Repeat("Ab12", 8)+"\n")
	fixture(t, filepath.Join(project, "big.pt"), "weights")
	fixture(t, filepath.Join(project, ".git", "objects", "pack", "x.pack"), strings.Repeat("safe", 20))
	fixture(t, filepath.Join(unselected, "private"), "must never be collected")
	fixture(t, filepath.Join(project, ".claude", ".credentials.json"), "private")
	// Windows may require elevated privileges for symlinks; the remaining safety checks still run.
	_ = os.Symlink(unselected, filepath.Join(project, "escape"))
	src := filepath.Join(home, "trace.jsonl")
	fixture(t, src, `{"type":"user","cwd":`+quoteJSON(project)+`,"timestamp":"2026-01-01T00:00:00Z"}`+"\n"+`{"type":"assistant","message":{"stop_reason":"end_turn","content":[]},"timestamp":"2026-01-01T00:01:00Z"}`+"\n")
	rules := Rules{LargeFileThreshold: 1024, Blacklist: []string{"*.pt"}}
	scanner, e := NewScanner()
	if e != nil {
		t.Fatal(e)
	}
	var hashes []string
	var archiveOrders [][]string
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
			archiveCRC, archiveSize, hashErr := CRC64File(a.LocalPath)
			if hashErr != nil || archiveCRC != a.CRC64 || archiveSize != a.Size {
				t.Fatalf("streamed archive crc/size mismatch: artifact=(%s,%d) actual=(%s,%d) err=%v", a.CRC64, a.Size, archiveCRC, archiveSize, hashErr)
			}
			hashes = append(hashes, a.CRC64)
			f, e := os.Open(a.LocalPath)
			if e != nil {
				t.Fatal(e)
			}
			z, e := gzip.NewReader(f)
			if e != nil {
				t.Fatal(e)
			}
			tr := tar.NewReader(z)
			names := map[string]bool{}
			var orderedNames []string
			for {
				h, e := tr.Next()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				names[h.Name] = true
				orderedNames = append(orderedNames, h.Name)
				if strings.Contains(h.Name, ".env") || strings.Contains(h.Name, "escape") || strings.Contains(h.Name, ".claude") || strings.Contains(h.Name, "big.pt") {
					t.Fatal("unsafe archive entry", h.Name)
				}
			}
			z.Close()
			f.Close()
			if !names[".git/objects/pack/x.pack"] || !names["main.go"] {
				t.Fatal(names)
			}
			wantOrder := []string{".git/", ".git/objects/", ".git/objects/pack/", ".git/objects/pack/x.pack", "go.mod", "main.go"}
			if !reflect.DeepEqual(orderedNames, wantOrder) {
				t.Fatalf("archive member order/structure changed: got %#v want %#v", orderedNames, wantOrder)
			}
			archiveOrders = append(archiveOrders, orderedNames)
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
	if !reflect.DeepEqual(archiveOrders[0], archiveOrders[1]) {
		t.Fatal("parallel staging changed directory or file order between archives")
	}
	data, _ := os.ReadFile(filepath.Join(project, ".env"))
	if string(data) != "api_key=ghp_"+strings.Repeat("Ab12", 8)+"\n" {
		t.Fatal("source modified")
	}
}

func quoteJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

func TestSecretInSelectedTrajectoryStillCollectsWorkspaceSnapshot(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "project")
	fixture(t, filepath.Join(project, "go.mod"), "module example\n")
	token := "ghp_" + strings.Repeat("Ab12", 8)
	source := filepath.Join(home, "trajectory.jsonl")
	fixture(t, source, `{"type":"user","cwd":`+quoteJSON(project)+`,"message":{"content":"`+token+`"}}`+"\n")

	manifest := &Manifest{
		JobID:       "job",
		Tool:        "claude",
		ConsentedAt: time.Now(),
		Rules:       Rules{LargeFileThreshold: 1 << 20},
		Sessions: []*Session{{
			Tool:        "claude",
			ID:          "session",
			Source:      source,
			Workspace:   Classify(project, home),
			Eligibility: "archive_only",
		}},
	}
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	collector := Collector{Dir: t.TempDir(), Home: home, Scanner: scanner}
	if err := collector.Collect(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Sessions[0].SkipReason != "secret_detected" {
		t.Fatalf("expected selected trajectory to be excluded by secret scan: %+v", manifest.Sessions[0])
	}
	if manifest.Summary.Workspaces != 1 || manifest.Summary.Excluded != 1 {
		t.Fatalf("unexpected collection summary: %+v", manifest.Summary)
	}
	kinds := map[string]int{}
	for _, artifact := range manifest.Artifacts {
		kinds[artifact.Kind]++
	}
	if kinds["trajectory"] != 0 || kinds["workspace"] != 1 || kinds["large_files"] != 1 || kinds["exclusions"] != 1 {
		t.Fatalf("trajectory exclusion suppressed independent workspace collection: %#v", kinds)
	}
}

func TestCollectorProcessesDistinctWorkspacesAndKeepsStableArtifacts(t *testing.T) {
	home := t.TempDir()
	workspaces := []string{filepath.Join(home, "project-a"), filepath.Join(home, "project-b")}
	sessions := make([]*Session, 0, len(workspaces))
	selection := make([]string, 0, len(workspaces))
	for i, workspace := range workspaces {
		fixture(t, filepath.Join(workspace, "go.mod"), fmt.Sprintf("module example%d\n", i))
		fixture(t, filepath.Join(workspace, "main.go"), fmt.Sprintf("package main\n// project %d\n", i))
		source := filepath.Join(home, fmt.Sprintf("trajectory-%d.jsonl", i))
		fixture(t, source, `{"type":"user","cwd":`+quoteJSON(workspace)+`,"message":{"content":"hello"}}`+"\n")
		id := fmt.Sprintf("session-%d", i)
		sessions = append(sessions, &Session{Tool: "claude", ID: id, Source: source, Workspace: Classify(workspace, home), Eligibility: "archive_only"})
		selection = append(selection, id)
	}
	rules := Rules{LargeFileThreshold: 1 << 20}
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	manifest := &Manifest{JobID: "parallel-job", Tool: "claude", ConsentedAt: time.Now(), Rules: rules, Sessions: sessions, Selection: selection}
	var mu sync.Mutex
	workspaceProgress := map[string]bool{}
	collector := Collector{Dir: t.TempDir(), Home: home, Scanner: scanner, Progress: func(key, stage string, _, _ int) {
		if strings.HasPrefix(stage, "扫描 workspace：") {
			mu.Lock()
			workspaceProgress[strings.TrimPrefix(stage, "扫描 workspace：")] = true
			mu.Unlock()
		}
	}}
	if err := collector.Collect(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Summary.Workspaces != 2 {
		t.Fatalf("expected two workspace snapshots: %+v", manifest.Summary)
	}
	if len(workspaceProgress) != 2 {
		t.Fatalf("workspace progress did not report both roots: %#v", workspaceProgress)
	}
	workspaceArtifacts := []string{}
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind == "workspace" {
			workspaceArtifacts = append(workspaceArtifacts, artifact.SessionIDs[0])
		}
	}
	wantArtifacts := []string{
		"session-0",
		"session-1",
	}
	if !reflect.DeepEqual(workspaceArtifacts, wantArtifacts) {
		t.Fatalf("workspace artifact order should follow sorted source paths: got %#v want %#v", workspaceArtifacts, wantArtifacts)
	}
}

func TestCollectorSharesWorkspaceAndUsesJobScopedArtifactKeys(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "shared-project")
	fixture(t, filepath.Join(project, "go.mod"), "module shared\n")
	rules := Rules{LargeFileThreshold: 1 << 20}
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	prefix := "trajectory/participant_id=participant-test/dt=2026-11-14/job=job-123/"
	sessions := make([]*Session, 0, 2)
	for _, id := range []string{"session-a", "session-b"} {
		source := filepath.Join(home, id+".jsonl")
		fixture(t, source, `{"type":"user","cwd":`+quoteJSON(project)+`,"message":{"content":"hello"}}`+"\n")
		sessions = append(sessions, &Session{Tool: "claude", ID: id, Source: source, Workspace: Classify(project, home), Eligibility: "archive_only"})
	}
	manifest := &Manifest{JobID: "job-123", Tool: "claude", ObjectPrefix: prefix, ConsentedAt: time.Now(), Rules: rules, Sessions: sessions, Selection: []string{"session-a", "session-b"}}
	collector := Collector{Dir: t.TempDir(), Home: home, Scanner: scanner}
	if err := collector.Collect(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, artifact := range manifest.Artifacts {
		counts[artifact.Kind]++
		if !strings.HasPrefix(artifact.Key, prefix) {
			t.Fatalf("artifact escaped job prefix: %s", artifact.Key)
		}
		switch artifact.Kind {
		case "trajectory":
			if !strings.HasSuffix(artifact.Key, "/sessions/claude/"+artifact.SessionIDs[0]+"/trajectory.jsonl") {
				t.Fatalf("unexpected trajectory key: %s", artifact.Key)
			}
		case "workspace":
			if !strings.HasSuffix(artifact.Key, "/workspaces/"+artifact.ID+"/workspace.tar.gz") || !reflect.DeepEqual(artifact.SessionIDs, []string{"session-a", "session-b"}) {
				t.Fatalf("unexpected shared workspace artifact: %+v", artifact)
			}
			for _, session := range manifest.Sessions {
				if session.WorkspaceArtifact != artifact.ID {
					t.Fatalf("session %s does not reference shared workspace %s", session.ID, artifact.ID)
				}
			}
		case "large_files":
			if !strings.HasSuffix(artifact.Key, "/workspaces/"+artifact.WorkspaceID+"/large-files.jsonl") {
				t.Fatalf("unexpected large-files key: %s", artifact.Key)
			}
		case "exclusions":
			if artifact.Key != prefix+"exclusions.jsonl" {
				t.Fatalf("unexpected exclusions key: %s", artifact.Key)
			}
		}
	}
	if counts["trajectory"] != 2 || counts["workspace"] != 1 || counts["large_files"] != 1 || counts["exclusions"] != 1 {
		t.Fatalf("unexpected shared workspace artifact counts: %#v", counts)
	}
}

func TestScannerCopyContextStopsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.CopyContext(ctx, io.Discard, strings.NewReader(strings.Repeat("x", 1<<20))); err != context.Canceled {
		t.Fatalf("CopyContext error = %v, want context cancellation", err)
	}
}

func TestScannerProgressReportsFinalPartialMiB(t *testing.T) {
	payload := strings.Repeat("x", (1<<20)+123)
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	var last int64
	hit, err := scanner.CopyContextProgress(context.Background(), io.Discard, strings.NewReader(payload), func(processed int64) {
		last = processed
	})
	if err != nil || hit {
		t.Fatalf("scan result: hit=%t err=%v", hit, err)
	}
	if last != int64(len(payload)) {
		t.Fatalf("final progress = %d, want exact file size %d", last, len(payload))
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
