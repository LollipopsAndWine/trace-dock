package app

import (
	"path/filepath"
	"testing"

	"github.com/trace-dock/trace-dock/internal/core"
)

func TestTrajectoryLocationHonorsExplicitNativeAndDefaultPaths(t *testing.T) {
	for _, tool := range []string{"codex", "claude"} {
		t.Run(tool, func(t *testing.T) {
			home := t.TempDir()
			native, trace, suffix, defaultFolder := "CODEX_HOME", "TRACE_DOCK_CODEX_HOME", "sessions", ".codex"
			if tool == "claude" {
				native, trace, suffix, defaultFolder = "CLAUDE_CONFIG_DIR", "TRACE_DOCK_CLAUDE_HOME", "projects", ".claude"
			}
			t.Setenv(native, "")
			t.Setenv(trace, "")
			check := func(flag string, cfg config, want, source string) {
				t.Helper()
				root, gotSource, err := trajectoryLocation(tool, home, flag, cfg)
				if err != nil || root != want || gotSource != source {
					t.Fatalf("location=%q source=%q err=%v, want %q (%s)", root, gotSource, err, want, source)
				}
			}
			check("", config{}, filepath.Join(home, defaultFolder, suffix), "默认数据目录")
			t.Setenv(native, filepath.Join(home, "native-data"))
			check("", config{}, filepath.Join(home, "native-data", suffix), native)
			cfg := config{CodexHome: filepath.Join(home, "configured-data"), ClaudeHome: filepath.Join(home, "configured-data")}
			check("", cfg, filepath.Join(home, "configured-data", suffix), "config.json")
			t.Setenv(trace, filepath.Join(home, "trace-override", suffix))
			check("", cfg, filepath.Join(home, "trace-override", suffix), trace)
			check(filepath.Join(home, "explicit data", suffix), cfg, filepath.Join(home, "explicit data", suffix), "--"+tool+"-home")
			check("~/chosen", cfg, filepath.Join(home, "chosen", suffix), "--"+tool+"-home")
		})
	}
}

func TestCustomClientDataIsDiscoveredButNeverSnapshotsAgentState(t *testing.T) {
	for _, tool := range []string{"codex", "claude"} {
		t.Run(tool, func(t *testing.T) {
			home := t.TempDir()
			dataHome := filepath.Join(home, "custom-client-data")
			suffix := "sessions"
			if tool == "claude" {
				suffix = "projects"
			}
			root, err := normalizeTrajectoryRoot(tool, home, dataHome)
			if err != nil || root != filepath.Join(dataHome, suffix) {
				t.Fatalf("normalized custom location=%q err=%v", root, err)
			}
			if err := core.WritePrivate(filepath.Join(dataHome, "go.mod"), []byte("module client-data\n")); err != nil {
				t.Fatal(err)
			}
			body := `{"type":"user","cwd":"` + filepath.ToSlash(dataHome) + `"}` + "\n"
			file := filepath.Join(root, "project", "session.jsonl")
			if tool == "codex" {
				body = `{"type":"session_meta","payload":{"cwd":"` + filepath.ToSlash(dataHome) + `"}}` + "\n" + `{"type":"response_item"}` + "\n"
				file = filepath.Join(root, "2026", "11", "15", "rollout-11111111-1111-1111-1111-111111111111.jsonl")
			}
			if err := core.WritePrivate(file, []byte(body)); err != nil {
				t.Fatal(err)
			}
			sessions, err := core.Discover(tool, home, root)
			if err != nil || len(sessions) != 1 || sessions[0].Eligibility == "excluded" {
				t.Fatalf("custom-client session not discovered: count=%d err=%v", len(sessions), err)
			}
			for _, s := range core.RefreshSelected(sessions, home) {
				if s.DataHome != dataHome || s.Workspace.Policy != "path_only" || (s.Workspace.Reason != "private_agent_or_job_state" && s.Workspace.Reason != "contains_private_agent_or_job_state") {
					t.Fatalf("custom agent state could become a project snapshot: %+v", s)
				}
			}
		})
	}
}
