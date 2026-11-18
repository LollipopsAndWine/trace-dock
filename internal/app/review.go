package app

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/trace-dock/trace-dock/internal/core"
	"github.com/trace-dock/trace-dock/internal/ui"
)

type uploadGroup struct {
	Files int
	Bytes int64
}

type uploadInventory struct {
	SelectedMain, SelectedSubagents, ReadyMain, ReadySubagents int
	Workspaces                                                 int
	SnapshotIDs                                                map[string]bool
	MissingMain, MissingSubagents                              []*core.Session
	Trajectories, Snapshots, Lists, Total                      uploadGroup
}

func inspectUpload(m *core.Manifest, manifestBytes int64) uploadInventory {
	r := uploadInventory{SelectedMain: len(m.Selection), Lists: uploadGroup{Files: 1, Bytes: manifestBytes}, SnapshotIDs: map[string]bool{}}
	artifacts := map[string]core.Artifact{}
	for _, a := range m.Artifacts {
		artifacts[a.ID] = a
		group := &r.Lists
		switch a.Kind {
		case "trajectory":
			group = &r.Trajectories
		case "workspace":
			group = &r.Snapshots
			r.Workspaces++
			r.SnapshotIDs[a.ID] = true
		}
		group.Files++
		group.Bytes += a.Size
	}
	var visit func(*core.Session, bool)
	visit = func(s *core.Session, child bool) {
		a, exists := artifacts[s.TrajectoryArtifact]
		ready := exists && a.Kind == "trajectory"
		if child {
			r.SelectedSubagents++
			if ready {
				r.ReadySubagents++
			} else {
				r.MissingSubagents = append(r.MissingSubagents, s)
			}
		} else if ready {
			r.ReadyMain++
		} else {
			r.MissingMain = append(r.MissingMain, s)
		}
		for _, nested := range s.Children {
			visit(nested, true)
		}
	}
	for _, s := range m.Sessions {
		visit(s, false)
	}
	r.Total = uploadGroup{
		Files: r.Trajectories.Files + r.Snapshots.Files + r.Lists.Files,
		Bytes: r.Trajectories.Bytes + r.Snapshots.Bytes + r.Lists.Bytes,
	}
	return r
}

func missingTrajectoryReason(s *core.Session) string {
	switch s.SkipReason {
	case "secret_detected":
		return "敏感信息排除"
	case "source_missing_or_changed", "source_unreadable", "trajectory_unreadable", "not_regular_or_unreadable":
		return "文件缺失或无法读取"
	case "changed_during_collect", "cwd_changed_after_selection":
		return "采集期间源文件或目录变化"
	case "scanner_or_copy_failed", "staging_failed":
		return "扫描或暂存失败"
	default:
		if s.SkipReason != "" {
			return exclusionReasonLabel(s.SkipReason)
		}
		return "未生成轨迹文件"
	}
}

func missingTrajectorySummary(sessions []*core.Session) string {
	counts := map[string]int{}
	for _, s := range sessions {
		counts[missingTrajectoryReason(s)]++
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s %d 份", key, counts[key]))
	}
	return strings.Join(parts, "；")
}

func uploadReviewSummaryLines(u *ui.UI, m *core.Manifest, r uploadInventory, exclusions map[string]int) []string {
	var lines []string
	add := func(label, value string) {
		lines = append(lines, ui.WrapLines(label+"  ", value)...)
	}
	add("主会话轨迹", fmt.Sprintf("上传 %d / 已选 %d 份 · %d 份不上传", r.ReadyMain, r.SelectedMain, len(r.MissingMain)))
	if r.SelectedSubagents > 0 {
		add("子 agent  ", fmt.Sprintf("上传 %d / 关联 %d 份 · %d 份不上传", r.ReadySubagents, r.SelectedSubagents, len(r.MissingSubagents)))
	}
	add("项目快照  ", fmt.Sprintf("上传 %d 份（按目录去重）", r.Workspaces))
	add("各类清单  ", fmt.Sprintf("上传 %d 份（含 manifest 和排除记录）", r.Lists.Files))
	add("预计总量  ", fmt.Sprintf("%s · %d 份文件", formatBytes(r.Total.Bytes), r.Total.Files))
	add("体积分项  ", fmt.Sprintf("轨迹 %s · 快照 %s · 清单 %s", formatBytes(r.Trajectories.Bytes), formatBytes(r.Snapshots.Bytes), formatBytes(r.Lists.Bytes)))
	secret := exclusions["secret_detected"]
	types, sizes := exclusions["large_file_type"], exclusions["large_file_size"]
	add("文件排除  ", fmt.Sprintf("敏感信息 %d 项 · 大文件 %d 项 · 其他 %d 项", secret, types+sizes, max(0, m.Summary.Excluded-secret-types-sizes)))
	if types+sizes > 0 {
		add("大文件    ", fmt.Sprintf("文件类型排除 %d 项 · 体积超限 %d 项，正文不上传", types, sizes))
	}
	missing := append(append([]*core.Session{}, r.MissingMain...), r.MissingSubagents...)
	if len(missing) > 0 {
		add("轨迹未上传", missingTrajectorySummary(missing))
	}
	if r.ReadyMain+r.ReadySubagents == 0 && r.Workspaces == 0 {
		lines = append(lines, u.Style("33", "本次只有清单，未包含轨迹或项目快照。"))
	}
	return lines
}

func uploadReviewLines(u *ui.UI, m *core.Manifest, inventory uploadInventory, exclusions map[string]int, manifestPath string) []string {
	r := inventory
	lines := []string{u.Style("32", "✓ 检测与打包完成，等待上传确认。")}
	add := func(prefix, value, color string) {
		for _, line := range ui.WrapLines(prefix, value) {
			lines = append(lines, u.Style(color, line))
		}
	}
	heading := func(title, color string) {
		lines = append(lines, "", u.Style(color, title))
	}
	heading("上传内容", "1;36")
	add("  主会话轨迹  ", fmt.Sprintf("上传 %d / 已选 %d 份 · %d 份不上传", r.ReadyMain, r.SelectedMain, len(r.MissingMain)), "")
	if r.SelectedSubagents > 0 {
		add("  子 agent    ", fmt.Sprintf("上传 %d / 关联 %d 份 · %d 份不上传", r.ReadySubagents, r.SelectedSubagents, len(r.MissingSubagents)), "")
	}
	add("  项目快照    ", fmt.Sprintf("上传 %d 份（按目录去重）", r.Workspaces), "")
	add("  各类清单    ", fmt.Sprintf("上传 %d 份（含 manifest 和排除记录）", r.Lists.Files), "")

	heading("上传体积（本次所选内容合计）", "1;36")
	add("  预计总量    ", fmt.Sprintf("%s · %d 份文件", formatBytes(r.Total.Bytes), r.Total.Files), "1")
	add("  构成        ", fmt.Sprintf("轨迹 %s · 压缩快照 %s · 清单 %s", formatBytes(r.Trajectories.Bytes), formatBytes(r.Snapshots.Bytes), formatBytes(r.Lists.Bytes)), "90")

	heading("排除情况", "1;33")
	missing := append(append([]*core.Session{}, r.MissingMain...), r.MissingSubagents...)
	for i, s := range missing[:min(3, len(missing))] {
		if i > 0 {
			lines = append(lines, "")
		}
		kind := "主会话"
		if i >= len(r.MissingMain) {
			kind = "子 agent"
		}
		add("  ", fmt.Sprintf("未上传%s轨迹（%d/%d）", kind, i+1, len(missing)), "1")
		add("    项目目录  ", reviewProjectName(s), "")
		add("    会话 ID   ", safe(s.ID), "")
		reason := missingTrajectoryReason(s)
		if s.SkipReason == "secret_detected" {
			reason = "检测到敏感信息"
		}
		add("    排除原因  ", reason, "33")
		if r.SnapshotIDs[s.WorkspaceArtifact] {
			add("    项目快照  ", "仍上传（已单独检测，命中文件已排除）", "")
		} else {
			add("    项目快照  ", "无待上传文件", "")
		}
	}
	if len(missing) > 3 {
		add("  ", fmt.Sprintf("其余 %d 份未上传轨迹见详细清单。", len(missing)-3), "90")
	}
	if m.Summary.Excluded > 0 {
		if len(missing) > 0 {
			lines = append(lines, "")
		}
		secret := exclusions["secret_detected"]
		types, sizes := exclusions["large_file_type"], exclusions["large_file_size"]
		large := types + sizes
		other := max(0, m.Summary.Excluded-secret-large)
		add("  ", fmt.Sprintf("全部排除记录：%d 项", m.Summary.Excluded), "1")
		add("    统计范围  ", "本次所有选中轨迹和去重后的项目目录", "90")
		add("    敏感信息  ", fmt.Sprintf("%d 项 · 整个文件不上传", secret), "")
		add("    大文件    ", fmt.Sprintf("%d 项（文件类型排除 %d 项 · 体积超限 %d 项）", large, types, sizes), "")
		if large > 0 {
			add("              ", "正文不上传，仅上传路径清单", "90")
		}
		add("    其他      ", fmt.Sprintf("%d 项 · 私有状态、链接等（详见清单）", other), "")
	} else if len(missing) == 0 {
		add("  ", "没有文件被排除。", "90")
	}
	if r.ReadyMain+r.ReadySubagents == 0 && r.Workspaces == 0 {
		add("  ", "本次只有清单，未包含轨迹或项目快照。", "33")
	}
	lines = append(lines, "")
	add("详细清单：", manifestPath, "90")
	return lines
}

func reviewProjectName(s *core.Session) string {
	workspace := s.Workspace.Raw
	if workspace == "" {
		workspace = s.Workspace.Normalized
	}
	name := path.Base(strings.ReplaceAll(workspace, `\`, "/"))
	if name == "." || name == "/" || name == "" {
		return "未知（轨迹中未取得项目路径）"
	}
	return safe(name)
}
