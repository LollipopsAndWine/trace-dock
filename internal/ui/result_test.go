package ui

import (
	"strings"
	"testing"
)

func TestCompletionReplacesUploadAndQuestionHistory(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, TTY: true}
	u.BeginFlow("Trace Dock")
	u.Record("确认向 OSS 上传本次选中数据？", "是")
	u.ProgressNamed("old-object", "对象 5/5 · manifest.json")
	u.BeginUpload(UploadSummary{TotalBytes: 248 << 20})
	u.VerifyUpload()
	output.Reset()
	u.CompleteCollection(CollectionResult{
		Server: "http://127.0.0.1:8080", JobID: "job-123", ReceiptPath: "/data/jobs/job-123/receipt.json",
		Discovered: 4, Selectable: 4, Selected: 2, UploadedMain: 2, UploadedSubagents: 2,
		SelectedSubagents: 2,
		Workspaces:        2, SecretExcluded: 2, Excluded: 5, Objects: 9, UploadedBytes: 248 << 20, TotalBytes: 248 << 20,
		OtherExclusionReasons: "私有 Agent 状态文件，不采集 3 项",
	})
	// A late update must not add the upload progress back to the completed screen.
	u.UploadProgress(248<<20, 248<<20)
	u.RefreshVerification()
	got := output.String()
	for _, unwanted := range []string{"确认向 OSS", "对象 5/5", "Ctrl+C", "正在校验", "MiB/s", "━━"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("completed screen retained %q: %s", unwanted, got)
		}
	}
	for _, want := range []string{"07/07", "采集完成 · 上传及校验通过", "2 / 4（另 2 个未选中）", "已上传 2 / 已选 2 份", "子 agent 轨迹", "Workspace 快照", "密钥排除文件", "3（合计 5 项）", "私有 Agent 状态文件，不采集 3 项", "248.00 MiB / 248.00 MiB", "receipt.json"} {
		if !strings.Contains(got, want) {
			t.Errorf("completed screen missing %q: %s", want, got)
		}
	}
	if u.upload != nil || len(u.history) != 0 || len(u.activities) != 0 {
		t.Fatal("completion retained upload or interaction state")
	}
}

func TestCompletionSeparatesUnselectedAndExcludedSessions(t *testing.T) {
	u := &UI{result: &CollectionResult{Selectable: 10, Selected: 3, UploadedMain: 2}}
	got := strings.Join(u.resultLines(100), "\n")
	if !strings.Contains(got, "另 7 个未选中") || !strings.Contains(got, "已上传 2 / 已选 3 份；排除 1 份") {
		t.Fatalf("selected but excluded sessions were miscounted: %s", got)
	}
	u.result.Selectable = 0
	got = strings.Join(u.resultLines(100), "\n")
	if strings.Contains(got, "未选中") {
		t.Fatalf("missing selectable count must not fabricate an unselected total: %s", got)
	}
}

func TestCompletionWrapsFullReceiptPathAtTerminalWidth(t *testing.T) {
	path := "/home/participant/.trace-dock/jobs/job-123/receipt.json"
	u := &UI{heading: "Trace Dock · 会话与 workspace 采集", result: &CollectionResult{JobID: "job-123", ReceiptPath: path}}
	for _, width := range []int{40, 80} {
		lines := u.resultLines(width)
		for _, line := range lines {
			if displayWidth(line) > width {
				t.Fatalf("line exceeds width %d: %q", width, line)
			}
		}
		flat := strings.ReplaceAll(strings.Join(lines, ""), " ", "")
		if !strings.Contains(flat, path) {
			t.Fatalf("receipt path truncated at width %d: %s", width, flat)
		}
	}
}
