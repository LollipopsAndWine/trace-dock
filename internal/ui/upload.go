package ui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

type UploadSummary struct {
	Server, Tool                                                string
	Selected, Discovered, Subagents, Workspaces, Excluded       int
	ReadyMain, ReadySubagents, SecretExcluded, LargeFileRecords int
	LargeFileTypeRecords                                        int
	UploadedBytes, TotalBytes                                   int64
}

type uploadView struct {
	summary       UploadSummary
	transferred   int64
	started       time.Time
	updated       time.Time
	verifyStarted time.Time
	verifying     bool
	interrupted   bool
}

func (u *UI) BeginUpload(summary UploadSummary) {
	now := time.Now()
	summary.TotalBytes = max(0, summary.TotalBytes)
	summary.UploadedBytes = min(max(0, summary.UploadedBytes), summary.TotalBytes)
	u.step, u.title = "06/07", "上传选中内容"
	u.result = nil
	u.info, u.activities, u.activity = nil, nil, ""
	u.upload = &uploadView{summary: summary, transferred: summary.UploadedBytes, started: now, updated: now}
	u.renderBase()
}

func (u *UI) UploadProgress(transferred int64) {
	if u.upload == nil {
		return
	}
	u.upload.transferred = min(max(u.upload.transferred, transferred), u.upload.summary.TotalBytes)
	u.upload.updated = time.Now()
	u.renderBase()
}

func (u *UI) VerifyUpload() {
	if u.upload == nil {
		return
	}
	u.upload.verifying = true
	u.upload.transferred = u.upload.summary.TotalBytes
	u.upload.verifyStarted = time.Now()
	u.upload.updated = u.upload.verifyStarted
	u.renderBase()
}

func (u *UI) RefreshVerification() {
	if u.upload == nil || !u.upload.verifying || u.upload.interrupted {
		return
	}
	u.upload.updated = time.Now()
	u.renderBase()
}

func (u *UI) InterruptUpload() {
	if u.upload == nil {
		return
	}
	u.upload.interrupted = true
	u.renderBase()
}

func (v *uploadView) rate() float64 {
	elapsed := v.updated.Sub(v.started).Seconds()
	if elapsed < 0.25 {
		return 0
	}
	return float64(v.transferred-v.summary.UploadedBytes) / elapsed
}

func (v *uploadView) progressBar(width int, style func(string, string) string) string {
	percent := 0
	if v.summary.TotalBytes > 0 {
		percent = int(float64(v.transferred) / float64(v.summary.TotalBytes) * 100)
	}
	barWidth := max(1, min(44, width-7))
	filled := percent * barWidth / 100
	return style("32", strings.Repeat("━", filled)) + style("90", strings.Repeat("─", barWidth-filled)) + style("36", fmt.Sprintf(" %3d%%", percent))
}

func (v *uploadView) stats() string {
	text := fmt.Sprintf("%s / %s", humanBytes(v.transferred), humanBytes(v.summary.TotalBytes))
	if v.interrupted {
		return text
	}
	if v.verifying {
		seconds := 0
		if !v.verifyStarted.IsZero() {
			seconds = max(0, int(v.updated.Sub(v.verifyStarted).Seconds()))
		}
		return fmt.Sprintf("已传输 %s · 正在校验 · 已等待 %d 分 %02d 秒", humanBytes(v.summary.TotalBytes), seconds/60, seconds%60)
	}
	if v.transferred >= v.summary.TotalBytes {
		return "已传输 " + humanBytes(v.transferred) + " · 等待服务端校验"
	}
	rate := v.rate()
	if rate <= 0 {
		return text + "   -- MiB/s   剩余时间计算中"
	}
	remaining := float64(v.summary.TotalBytes-v.transferred) / rate
	eta := "不足 1 秒"
	if remaining >= 1 {
		seconds := int64(math.Ceil(remaining))
		switch {
		case seconds >= 3600:
			eta = fmt.Sprintf("%d 小时 %d 分", seconds/3600, seconds%3600/60)
		case seconds >= 60:
			eta = fmt.Sprintf("%d 分 %d 秒", seconds/60, seconds%60)
		default:
			eta = fmt.Sprintf("%d 秒", seconds)
		}
	}
	return text + fmt.Sprintf("   %.1f MiB/s   剩余约 %s", rate/(1<<20), eta)
}

func (u *UI) renderUpload() {
	u.renderScreen(u.uploadLines(u.screenWidth()))
}

func (u *UI) screenWidth() int {
	width := 80
	if output, ok := u.Out.(*os.File); ok {
		if columns, _, err := term.GetSize(int(output.Fd())); err == nil && columns > 0 {
			width = columns
		}
	}
	return width
}

func (u *UI) renderScreen(lines []string) {
	end := u.lineEnd()
	if u.TTY {
		end = "\x1b[K" + end
	}
	screen := strings.Join(lines, end) + end
	if u.TTY {
		// Replace the screen at once and erase line tails without a blank frame.
		screen = "\x1b[H" + screen + "\x1b[J"
	}
	_, _ = fmt.Fprint(u.Out, screen)
}

func (u *UI) uploadLines(width int) []string {
	v := u.upload
	s := v.summary
	check := u.Style("32", "✓") + " "
	lines := []string{}
	add := func(text, color string) {
		for _, line := range wrapText(text, max(1, width)) {
			lines = append(lines, u.Style(color, line))
		}
	}
	add(clean(u.heading), "1;36")
	lines = append(lines, "")
	if v.verifying {
		add("06/07 · 服务端校验（尚未完成）", "1;36")
	} else {
		add("06/07 · 上传选中内容", "2")
	}
	lines = append(lines, "")
	addCheck := func(label, value string) {
		prefix := check
		indent := "  "
		text := value
		if label == "" || value == "" {
			text = label + value
		} else {
			prefix += label + "  "
			indent += strings.Repeat(" ", displayWidth(label)+2)
		}
		for i, line := range wrapText(text, max(1, width-displayWidth(indent))) {
			if i == 0 {
				lines = append(lines, prefix+u.Style("36", line))
			} else {
				lines = append(lines, indent+u.Style("36", line))
			}
		}
	}
	addCheck("服务器", clean(s.Server))
	addCheck("邀请代码", "验证通过")
	addCheck("采集工具", clean(s.Tool))
	addCheck("已选范围", fmt.Sprintf("%d / %d 个主会话 · %d 个子 agent", s.Selected, s.Discovered, s.Subagents))
	const uploadLabel = "本次实际上传"
	addCheck(uploadLabel, fmt.Sprintf("主会话轨迹 %d/%d 份 · 子 agent 轨迹 %d/%d 份 · workspace 快照 %d 份", s.ReadyMain, s.Selected, s.ReadySubagents, s.Subagents, s.Workspaces))
	if s.ReadyMain < s.Selected || s.ReadySubagents < s.Subagents {
		indent := strings.Repeat(" ", 2+displayWidth(uploadLabel)+2)
		note := fmt.Sprintf("已排除 %d 份主会话轨迹、%d 份子 agent 轨迹，继续上传其余内容。", max(0, s.Selected-s.ReadyMain), max(0, s.Subagents-s.ReadySubagents))
		for _, line := range wrapText(note, max(1, width-displayWidth(indent))) {
			lines = append(lines, indent+u.Style("33", line))
		}
	}
	addCheck("用户已确认上传", "")
	lines = append(lines, "")
	addCheck("本机扫描完成", fmt.Sprintf("敏感信息排除 %d 项 · 大文件仅记路径 %d 项（文件类型排除 %d 项 · 体积超限 %d 项） · 其他排除 %d 项", s.SecretExcluded, s.LargeFileRecords, s.LargeFileTypeRecords, max(0, s.LargeFileRecords-s.LargeFileTypeRecords), max(0, s.Excluded-s.SecretExcluded-s.LargeFileRecords)))
	if s.Workspaces > 0 {
		addCheck("Workspace 归档与 manifest 已生成", "")
	} else {
		addCheck("采集结果与 manifest 已生成", "")
	}
	lines = append(lines, "")
	status := "正在直接上传到 OSS…"
	if v.verifying {
		status = "服务端校验中…"
	} else if v.transferred >= s.TotalBytes {
		status = "文件传输已结束，准备服务端校验…"
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	frame := frames[int(v.updated.Sub(v.started)/(120*time.Millisecond))%len(frames)]
	if v.interrupted {
		frame, status = "Ⅱ", "上传或校验已中断，本地任务已保留"
	}
	add(frame+" "+status, "1;36")
	if !v.verifying && v.transferred < s.TotalBytes {
		lines = append(lines, v.progressBar(width, u.Style))
	}
	add(v.stats(), "90")
	switch {
	case v.interrupted:
		add("再次运行可恢复任务并重试上传或校验。", "90")
	case v.verifying || v.transferred >= s.TotalBytes:
		add("任务尚未完成，请保持终端开启。", "1;33")
		add("显示“校验通过”的完成结果并生成回执后，才可关闭终端。", "90")
	default:
		add("Ctrl+C 暂停；保留本地任务，之后可恢复上传。", "90")
	}
	return lines
}
