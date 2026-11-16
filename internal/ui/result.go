package ui

import (
	"fmt"
	"strings"
)

type CollectionResult struct {
	Server, JobID, ReceiptPath                  string
	Discovered, Selectable, Selected            int
	UploadedMain, UploadedSubagents, Workspaces int
	SelectedSubagents                           int
	MainExclusionReasons, ChildExclusionReasons string
	SecretExcluded, Excluded, Invalid, Objects  int
	LargeFileRecords, LargeFileTypeRecords      int
	UploadedBytes, TotalBytes                   int64
}

func (u *UI) CompleteCollection(result CollectionResult) {
	u.step, u.title = "07/07", "所选数据上传及校验完成"
	u.history, u.info, u.activities = nil, nil, nil
	u.activity, u.upload = "", nil
	u.plainScreen = nil
	u.result = &result
	u.renderBase()
}

func (u *UI) renderResult() {
	u.renderScreen(u.resultLines(u.screenWidth()))
}

func (u *UI) resultLines(width int) []string {
	r := u.result
	lines := []string{}
	add := func(text, color string) {
		for _, line := range wrapText(text, max(1, width)) {
			lines = append(lines, u.Style(color, line))
		}
	}
	add(clean(u.heading), "1;36")
	lines = append(lines, "")
	add("07/07 · 所选数据上传及校验完成", "2")
	lines = append(lines, "")
	for _, text := range []string{"服务器  " + clean(r.Server), "邀请代码  验证通过"} {
		for i, line := range wrapText(text, max(1, width-2)) {
			prefix := "  "
			if i == 0 {
				prefix = u.Style("32", "✓") + " "
			}
			lines = append(lines, prefix+u.Style("36", line))
		}
	}
	lines = append(lines, "")
	if r.UploadedMain < r.Selected || r.UploadedSubagents < r.SelectedSubagents {
		add("✓ 本次实际上传内容已校验通过", "1;32")
	} else {
		add("✓ 采集完成 · 上传及校验通过", "1;32")
	}
	lines = append(lines, "")
	row := func(label, value, color string) {
		const column = 16
		prefix := label + strings.Repeat(" ", max(1, column-displayWidth(label))) + "  "
		for i, line := range wrapText(value, max(1, width-displayWidth(prefix))) {
			if i == 0 {
				lines = append(lines, u.Style("90", prefix)+u.Style(color, line))
			} else {
				lines = append(lines, strings.Repeat(" ", displayWidth(prefix))+u.Style(color, line))
			}
		}
	}
	row("Job ID", clean(r.JobID), "36")
	selection := fmt.Sprintf("%d 个", r.Selected)
	if r.Selectable > 0 {
		selection = fmt.Sprintf("%d / %d（另 %d 个未选中）", r.Selected, r.Selectable, max(0, r.Selectable-r.Selected))
	}
	row("已选主会话", selection, "")
	main := fmt.Sprintf("已上传 %d / 已选 %d 份", r.UploadedMain, r.Selected)
	if r.MainExclusionReasons != "" {
		main += "；" + r.MainExclusionReasons
	} else if r.Selected > r.UploadedMain {
		main += fmt.Sprintf("；排除 %d 份", r.Selected-r.UploadedMain)
	}
	row("主会话轨迹", main, "")
	children := fmt.Sprintf("已上传 %d / 关联 %d 份", r.UploadedSubagents, r.SelectedSubagents)
	if r.ChildExclusionReasons != "" {
		children += "；" + r.ChildExclusionReasons
	}
	row("子 agent 轨迹", children, "")
	row("Workspace 快照", fmt.Sprintf("%d 份（按目录去重）", r.Workspaces), "")
	row("密钥排除文件", fmt.Sprint(r.SecretExcluded), "")
	if r.LargeFileRecords > 0 {
		row("大文件清单", fmt.Sprintf("%d 项（文件类型排除 %d 项 · 体积超限 %d 项）", r.LargeFileRecords, r.LargeFileTypeRecords, max(0, r.LargeFileRecords-r.LargeFileTypeRecords)), "")
	}
	if other := r.Excluded - r.SecretExcluded - r.LargeFileRecords; other > 0 {
		row("其他排除项", fmt.Sprintf("%d（合计 %d 项）", other, r.Excluded), "")
	}
	row("发现/选中/无效", fmt.Sprintf("%d / %d / %d 个主会话", r.Discovered, r.Selected, r.Invalid), "")
	row("上传数据量", fmt.Sprintf("%s / %s", resultBytes(r.UploadedBytes), resultBytes(r.TotalBytes)), "")
	row("已校验对象", fmt.Sprintf("%d 个", r.Objects), "")
	lines = append(lines, "")
	for i, line := range wrapText(clean(r.ReceiptPath), max(1, width-9)) {
		prefix := "         "
		if i == 0 {
			prefix = "Receipt: "
		}
		lines = append(lines, u.Style("94", prefix+line))
	}
	return lines
}

func resultBytes(size int64) string {
	if size >= 1<<20 {
		return fmt.Sprintf("%.2f MiB", float64(size)/(1<<20))
	}
	if size >= 1024 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}
	return fmt.Sprintf("%d B", size)
}
