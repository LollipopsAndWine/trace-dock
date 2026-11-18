package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/trace-dock/trace-dock/internal/core"
	"github.com/trace-dock/trace-dock/internal/ui"
)

func TestUploadReviewSeparatesSelectedTrajectoriesSnapshotsAndTotalSize(t *testing.T) {
	m := &core.Manifest{
		Selection: []string{"root-one", "root-two"},
		Sessions: []*core.Session{
			{ID: "root-one", TrajectoryArtifact: "trace-one", WorkspaceArtifact: "shared", Children: []*core.Session{
				{ID: "child-one", TrajectoryArtifact: "trace-child"},
				{ID: "child-two", SkipReason: "scanner_or_copy_failed"},
			}},
			{ID: "root-two", WorkspaceArtifact: "shared", SkipReason: "secret_detected"},
		},
		Artifacts: []core.Artifact{
			{ID: "trace-one", Kind: "trajectory", Size: 100},
			{ID: "trace-child", Kind: "trajectory", Size: 200},
			{ID: "shared", Kind: "workspace", Size: 1024},
			{ID: "large-files", Kind: "large_files", Size: 50},
			{ID: "exclusions", Kind: "exclusions", Size: 60},
		},
		Summary: core.Summary{Subagents: 99, Workspaces: 99, Excluded: 4},
	}
	r := inspectUpload(m, 70)
	if r.SelectedMain != 2 || r.ReadyMain != 1 || len(r.MissingMain) != 1 || r.SelectedSubagents != 2 || r.ReadySubagents != 1 || len(r.MissingSubagents) != 1 {
		t.Fatalf("selected and archived counts were conflated: %+v", r)
	}
	if r.Workspaces != 1 || !r.SnapshotIDs["shared"] {
		t.Fatalf("shared workspace was double counted or lost with excluded trajectory: %+v", r)
	}
	if r.Trajectories.Bytes != 300 || r.Snapshots.Bytes != 1024 || r.Lists.Bytes != 180 || r.Lists.Files != 3 || r.Total.Bytes != 1504 || r.Total.Files != 6 {
		t.Fatalf("total must include each artifact and one manifest exactly once: %+v", r)
	}
	text := strings.Join(uploadReviewLines(&ui.UI{}, m, r, map[string]int{"secret_detected": 1, "large_file_type": 1, "large_file_size": 1, "private_agent_state": 1}, "/jobs/job/manifest.json"), "\n")
	for _, want := range []string{
		"主会话轨迹  上传 1 / 已选 2 份 · 1 份不上传",
		"子 agent    上传 1 / 关联 2 份 · 1 份不上传",
		"项目快照    上传 1 份（按目录去重）",
		"预计总量    1.5 KiB · 6 份文件",
		"轨迹 300 B · 压缩快照 1.0 KiB · 清单 180 B",
		"未上传主会话轨迹（1/2）",
		"项目目录  未知（轨迹中未取得项目路径）",
		"会话 ID   root-two",
		"排除原因  检测到敏感信息",
		"项目快照  仍上传（已单独检测，命中文件已排除）",
		"项目快照  无待上传文件",
		"全部排除记录：4 项",
		"统计范围  本次所有选中轨迹和去重后的项目目录",
		"敏感信息  1 项 · 整个文件不上传",
		"大文件    2 项（文件类型排除 1 项 · 体积超限 1 项）",
		"正文不上传，仅上传路径清单",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("review missing %q: %s", want, text)
		}
	}
}

func TestUploadReviewPreservesFullSessionIdentity(t *testing.T) {
	id := "019fc6b7-86df-7631-83ee-4afa12a58374"
	s := &core.Session{ID: id, SkipReason: "secret_detected", Workspace: core.Workspace{Raw: "/projects/pdf-postprocessing-fork"}}
	m := &core.Manifest{Selection: []string{id}, Sessions: []*core.Session{s}}
	text := strings.Join(uploadReviewLines(&ui.UI{}, m, inspectUpload(m, 0), nil, "/jobs/job/manifest.json"), "\n")
	if !strings.Contains(text, "项目目录  pdf-postprocessing-fork") || !strings.Contains(text, "会话 ID   "+id) || strings.Contains(text, "…") {
		t.Fatalf("review truncated or failed to label session identity: %s", text)
	}
}

func TestUploadReviewBoundsOmittedSessionDetailsForLargeSelections(t *testing.T) {
	m := &core.Manifest{}
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("root-%03d", i)
		m.Selection = append(m.Selection, id)
		s := &core.Session{ID: id}
		if i < 50 {
			s.TrajectoryArtifact = id
			m.Artifacts = append(m.Artifacts, core.Artifact{ID: id, Kind: "trajectory", Size: 10})
		} else {
			s.SkipReason = "secret_detected"
		}
		m.Sessions = append(m.Sessions, s)
	}
	r := inspectUpload(m, 10)
	text := strings.Join(uploadReviewLines(&ui.UI{}, m, r, nil, "/jobs/job/manifest.json"), "\n")
	if !strings.Contains(text, "上传 50 / 已选 100 份 · 50 份不上传") || !strings.Contains(text, "其余 47 份未上传轨迹见详细清单") {
		t.Fatalf("large selection lost totals or bounded details: %s", text)
	}
	if strings.Contains(text, "root-053") || strings.Contains(text, "root-099") {
		t.Fatalf("review printed an unbounded list of excluded sessions: %s", text)
	}
}

func TestUploadReviewSummaryIncludesCountsVolumeAndExclusionsTogether(t *testing.T) {
	m := &core.Manifest{
		Selection: []string{"one", "two", "three"},
		Sessions: []*core.Session{
			{ID: "one", TrajectoryArtifact: "trace-one"},
			{ID: "two", TrajectoryArtifact: "trace-two"},
			{ID: "three", SkipReason: "secret_detected"},
		},
		Artifacts: []core.Artifact{
			{ID: "trace-one", Kind: "trajectory", Size: 100},
			{ID: "trace-two", Kind: "trajectory", Size: 200},
			{ID: "snapshot-one", Kind: "workspace", Size: 1024},
			{ID: "snapshot-two", Kind: "workspace", Size: 1024},
			{ID: "snapshot-three", Kind: "workspace", Size: 1024},
			{ID: "exclusions", Kind: "exclusions", Size: 10},
		},
		Summary: core.Summary{Excluded: 4},
	}
	lines := uploadReviewSummaryLines(&ui.UI{}, m, inspectUpload(m, 100), map[string]int{
		"secret_detected": 1, "large_file_type": 1, "large_file_size": 1,
	})
	text := strings.Join(lines, "\n")
	for _, want := range []string{
		"上传 2 / 已选 3 份 · 1 份不上传", "上传 3 份（按目录去重）",
		"预计总量", "体积分项", "敏感信息 1 项 · 大文件 2 项 · 其他 1 项",
		"文件类型排除 1 项 · 体积超限 1 项", "敏感信息排除 1 份",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("confirmation summary missing %q: %s", want, text)
		}
	}
	if len(lines) > 10 || strings.Contains(text, "…") {
		t.Fatalf("confirmation summary is not compact: %s", text)
	}
}
