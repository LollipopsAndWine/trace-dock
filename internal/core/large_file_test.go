package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func revisedLargeFileRules() Rules {
	return Rules{
		LargeFileVersion:   LargeFileRulesVersion,
		LargeFileThreshold: 1 << 30,
		Whitelist:          []string{".git/**", "*.analysis.json"},
		Blacklist:          []string{"datasets/**", "checkpoints/**", "*.pt", "*.pth", "*.safetensors", "*.ckpt", "*.onnx", "*.gguf", "pytorch_model*.bin", "*.parquet", "*.arrow"},
	}
}

func TestLargeFileRulesPreserveDependencyConfigurationAndSmallArchives(t *testing.T) {
	rules := revisedLargeFileRules()
	for _, tc := range []struct {
		path   string
		size   int64
		reason string
	}{
		{".venv/lib/python3.13/site-packages/botocore/data/service-2.json.gz", 28270, ""},
		{".venv/lib/python3.13/site-packages/_virtualenv.pth", 18, ""},
		{"venv/lib/python3.13/dist-packages/custom.pth", 18, ""},
		{".venv/lib/python3.13/site-packages/package/models/model.pth", 4096, "large_file_type"},
		{".venv/lib/python3.13/site-packages/model.pth", 1<<30 + 1, "large_file_size"},
		{"model.pth", 18, "large_file_type"},
		{"models/weights.pt", 4096, "large_file_type"},
		{"models/weights.safetensors", 4096, "large_file_type"},
		{"pytorch_model-00001-of-00002.bin", 1024, "large_file_type"},
		{"small.bin", 1024, ""},
		{"model.gguf", 4096, "large_file_type"},
		{"datasets/train.json.gz", 1237, "large_file_type"},
		{"checkpoints/model.zip", 1237, "large_file_type"},
		{"archives/source.zip", 1237, ""},
		{"archives/source.tar", 1237, ""},
		{"archives/source.gz", 1237, ""},
		{"archives/source.zst", 1237, ""},
		{"archives/source.7z", 1237, ""},
		{"data.csv", 1<<30 - 1, ""},
		{"data.csv", 1 << 30, ""},
		{"data.csv", 1<<30 + 1, "large_file_size"},
		{"weights.pt", 1<<30 + 1, "large_file_type"},
		{"checkpoints/weights.analysis.json", 1<<30 + 1, ""},
		{".git/objects/pack/a.pack", 1<<30 + 1, ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if reason := LargeFileReason(tc.path, tc.size, rules); reason != tc.reason {
				t.Fatalf("reason=%q, want %q (bytes=%d)", reason, tc.reason, tc.size)
			}
		})
	}
}

func TestLargeFileCollectionRecordsTypeAndSizeWithoutChangingListSchema(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "project")
	fixture(t, filepath.Join(project, "go.mod"), "module project\n")
	fixture(t, filepath.Join(project, "weights.pt"), "small weights")
	fixture(t, filepath.Join(project, "data.csv"), "this content exceeds the test threshold")
	fixture(t, filepath.Join(project, "small.json.gz"), "small")
	source := filepath.Join(home, "trajectory.jsonl")
	body := `{"type":"user","cwd":` + quoteJSON(project) + `,"message":{"content":"hello"}}` + "\n"
	fixture(t, source, body)
	rules := revisedLargeFileRules()
	rules.LargeFileThreshold = 32
	scanner, err := NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	m := &Manifest{
		JobID: "job", Tool: "claude", Rules: rules, ConsentedAt: time.Now(),
		Sessions:  []*Session{{Tool: "claude", ID: "session", Source: source, Workspace: Classify(project, home), Eligibility: "archive_only"}},
		Selection: []string{"session"},
	}
	c := Collector{Dir: t.TempDir(), Home: home, Scanner: scanner}
	if err := c.Collect(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, excluded := range c.Exclusions {
		counts[excluded.Reason]++
	}
	if counts["large_file_type"] != 1 || counts["large_file_size"] != 1 || len(counts) != 2 || m.Summary.LargeFiles != 2 {
		t.Fatalf("type and size exclusions were conflated or double counted: %#v summary=%+v", counts, m.Summary)
	}
	for _, artifact := range m.Artifacts {
		if artifact.Kind != "large_files" {
			continue
		}
		data, err := os.ReadFile(artifact.LocalPath)
		if err != nil {
			t.Fatal(err)
		}
		var entries []map[string]any
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			var row map[string]any
			if err := json.Unmarshal(line, &row); err != nil {
				t.Fatal(err)
			}
			entries = append(entries, row)
		}
		if len(entries) != 2 {
			t.Fatalf("list did not include both exclusion causes: %#v", entries)
		}
		for _, row := range entries {
			if len(row) != 2 || row["relative_path"] == nil {
				t.Fatal("large-file JSONL schema changed", row)
			}
			if _, ok := row["remote_path"]; !ok {
				t.Fatal("large-file JSONL lost remote_path")
			}
		}
	}
}
