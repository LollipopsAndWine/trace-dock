package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnrecognizedTrajectoriesArchiveOriginalBytes(t *testing.T) {
	for _, tc := range []struct {
		name, tool, body, schema string
	}{
		{"future Codex", "codex", "{\"type\":\"future_event\",\"data\":{\"message\":\"hello\"}}\n", "unknown"},
		{"mixed Codex", "codex", "{\"type\":\"response_item\"}\n{\"payload\":{\"type\":\"item_completed\"}}\n", "codex.mixed"},
		{"future Claude", "claude", "{\"type\":\"future_event\",\"body\":\"hello\"}\n", "unknown"},
		{"damaged JSONL", "codex", "{incomplete JSON\nraw event data\n", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			source := filepath.Join(home, "rollout-11111111-1111-1111-1111-111111111111.jsonl")
			fixture(t, source, tc.body)
			session := parseSession(tc.tool, source, int64(len(tc.body)), home)
			if session.Schema != tc.schema || session.Eligibility == "excluded" || session.SkipReason != "" {
				t.Fatalf("format blocked raw collection: %+v", session)
			}
			scanner, err := NewScanner()
			if err != nil {
				t.Fatal(err)
			}
			manifest := &Manifest{
				JobID: "job", Tool: tc.tool, ConsentedAt: time.Now(),
				Rules:    Rules{LargeFileThreshold: 1 << 20},
				Sessions: []*Session{session}, Selection: []string{session.ID},
			}
			collector := Collector{Dir: t.TempDir(), Home: home, Scanner: scanner}
			if err := collector.Collect(context.Background(), manifest); err != nil {
				t.Fatal(err)
			}
			trajectories := 0
			for _, artifact := range manifest.Artifacts {
				if artifact.Kind != "trajectory" {
					continue
				}
				trajectories++
				staged, err := os.ReadFile(artifact.LocalPath)
				if err != nil || !bytes.Equal(staged, []byte(tc.body)) || artifact.CRC64 == "" {
					t.Fatalf("trajectory was normalized or rewritten: err=%v", err)
				}
			}
			if trajectories != 1 || manifest.Summary.Excluded != 0 || manifest.Summary.Workspaces != 0 {
				t.Fatalf("unexpected raw archive result: %+v", manifest.Summary)
			}
			if manifest.Sessions[0].Workspace.Policy != "path_only" {
				t.Fatal("unknown cwd expanded workspace collection")
			}
			original, err := os.ReadFile(source)
			if err != nil || string(original) != tc.body {
				t.Fatal("source trajectory changed")
			}
		})
	}
}

func TestUnknownFormatStillRejectsSecrets(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "rollout-11111111-1111-1111-1111-111111111111.jsonl")
	body := "{\"type\":\"future_event\",\"password\":\"UnitPass9#fixture\"}\n"
	fixture(t, source, body)
	session := parseSession("codex", source, int64(len(body)), home)
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	manifest := &Manifest{JobID: "job", Tool: "codex", ConsentedAt: time.Now(), Rules: Rules{LargeFileThreshold: 1 << 20}, Sessions: []*Session{session}, Selection: []string{session.ID}}
	collector := Collector{Dir: t.TempDir(), Home: home, Scanner: scanner}
	if err := collector.Collect(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Sessions[0].SkipReason != "secret_detected" || manifest.Summary.Excluded != 1 {
		t.Fatalf("unknown format bypassed secret detection: %+v", manifest.Sessions[0])
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.Kind == "trajectory" {
			t.Fatal("trajectory containing a secret entered the upload set")
		}
	}
	staged := filepath.Join(collector.Dir, "staging", "trajectory-"+Digest([]byte(source))[:24]+".jsonl")
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatal("rejected staging copy was retained")
	}
	for _, name := range []string{"manifest.json", "exclusions.jsonl"} {
		data, err := os.ReadFile(filepath.Join(collector.Dir, name))
		if err != nil || bytes.Contains(data, []byte("UnitPass9#fixture")) {
			t.Fatalf("secret leaked into %s: err=%v", name, err)
		}
	}
	original, err := os.ReadFile(source)
	if err != nil || string(original) != body {
		t.Fatal("source trajectory changed")
	}
}

func TestUnknownCodexFilenameUsesStableDistinctFileIDs(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".codex", "sessions")
	for _, day := range []string{"01", "02"} {
		fixture(t, filepath.Join(root, "2026", "11", day, "rollout-future.jsonl"), "{\"type\":\"future_event\"}\n")
	}
	sessions, err := Discover("codex", home, root)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("future filenames were dropped or merged: count=%d err=%v", len(sessions), err)
	}
	seen := map[string]bool{}
	for _, session := range sessions {
		if !strings.HasPrefix(session.ID, "file-") || seen[session.ID] || session.Eligibility == "excluded" {
			t.Fatalf("invalid fallback file identity: %+v", session)
		}
		seen[session.ID] = true
		refreshed := RefreshSelected([]*Session{session}, home)
		if len(refreshed) != 1 || refreshed[0].ID != session.ID || refreshed[0].Eligibility == "excluded" {
			t.Fatal("fallback file identity changed during collection")
		}
	}
	missing := parseSession("codex", filepath.Join(root, "rollout-missing.jsonl"), 0, home)
	if missing.Eligibility != "excluded" || missing.SkipReason != "trajectory_unreadable" {
		t.Fatal("unreadable trajectory became selectable")
	}
}
