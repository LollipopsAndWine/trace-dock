package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/trace-dock/trace-dock/internal/core"
	"github.com/trace-dock/trace-dock/internal/upload"
)

func TestCollectionResultCountsActualArchivesAndSecretExclusions(t *testing.T) {
	dir := t.TempDir()
	exclusions := filepath.Join(dir, "exclusions.jsonl")
	if err := os.WriteFile(exclusions, []byte("{\"reason\":\"secret_detected\"}\n{\"reason\":\"large_file_type\"}\n{\"reason\":\"private_agent_state\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reasons, err := exclusionReasonCounts(exclusions)
	if err != nil {
		t.Fatal(err)
	}
	m := &core.Manifest{
		JobID: "job-123", Discovered: 271, Selectable: 138, Selection: []string{"root-one", "root-two"},
		Sessions: []*core.Session{
			{ID: "root-one", TrajectoryArtifact: "trajectory-one", Children: []*core.Session{
				{ID: "child-one", TrajectoryArtifact: "trajectory-child"}, {ID: "excluded-child"},
			}},
			{ID: "root-two"},
		},
		Artifacts: []core.Artifact{{ID: "trajectory-one", Kind: "trajectory"}, {ID: "trajectory-child", Kind: "trajectory"}, {ID: "workspace", Kind: "workspace"}, {ID: "large-files", Kind: "large_files"}},
		Summary:   core.Summary{Selected: 2, Subagents: 2, Invalid: 1, Excluded: 3},
	}
	state := &upload.State{Objects: []upload.Object{{Artifact: core.Artifact{Size: 1024}}, {Artifact: core.Artifact{Size: 2048}}}}
	r := collectionResult(m, state, "https://collector.example.com", "/data/receipt.json", reasons)
	if r.UploadedMain != 1 || r.UploadedSubagents != 1 || r.Workspaces != 1 {
		t.Fatalf("result counted selected items instead of uploaded archives: %+v", r)
	}
	if r.SecretExcluded != 1 || r.Excluded != 3 || r.Selected != 2 || r.Selectable != 138 || r.Discovered != 271 {
		t.Fatalf("result conflated secret exclusions or discovery totals: %+v", r)
	}
	if r.OtherExclusionReasons != "私有 Agent 状态文件，不采集 1 项" {
		t.Fatalf("other exclusion reasons were not summarized: %q", r.OtherExclusionReasons)
	}
	if r.UploadedBytes != 3072 || r.TotalBytes != 3072 || r.Objects != 2 {
		t.Fatalf("result upload totals incorrect: %+v", r)
	}
}
