package ui

import (
	"strings"
	"testing"
	"time"
)

func TestUploadRateExcludesPreviouslyUploadedBytes(t *testing.T) {
	started := time.Unix(1, 0)
	v := &uploadView{
		summary:     UploadSummary{UploadedBytes: 80 << 20, TotalBytes: 100 << 20},
		transferred: 84 << 20, started: started, updated: started.Add(2 * time.Second),
	}
	if got := v.rate(); got != 2<<20 {
		t.Fatalf("resumed transfer rate = %v, want 2 MiB/s", got)
	}
	stats := v.stats()
	if !strings.Contains(stats, "2.0 MiB/s") || !strings.Contains(stats, "剩余约 8 秒") {
		t.Fatalf("incorrect resumed transfer statistics: %s", stats)
	}
	v.updated = started.Add(4 * time.Second)
	if v.rate() != 1<<20 {
		t.Fatalf("rate should reflect time spent waiting for the next part: %s", v.stats())
	}
}

func TestUploadProgressClampsStaleCallbacksAndSeparatesVerification(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, TTY: true}
	u.BeginUpload(UploadSummary{Selected: 1, Discovered: 138, TotalBytes: 100 << 20})
	u.UploadProgress(32 << 20)
	u.UploadProgress(16 << 20)
	if u.upload.transferred != 32<<20 {
		t.Fatal("stale callback moved upload progress backward")
	}
	u.UploadProgress(120 << 20)
	if u.upload.transferred != 100<<20 {
		t.Fatal("upload progress exceeded the total size")
	}
	u.VerifyUpload()
	stats := u.upload.stats()
	if !strings.Contains(stats, "正在校验") || strings.Contains(stats, "剩余约") {
		t.Fatalf("verification should not show a transfer ETA: %s", stats)
	}
	u.upload.verifyStarted = u.upload.updated.Add(-65 * time.Second)
	if !strings.Contains(u.upload.stats(), "已等待 1 分 05 秒") {
		t.Fatalf("verification did not report its own elapsed time: %s", u.upload.stats())
	}
	u.Step("07/07", "完成")
	if u.upload != nil {
		t.Fatal("completed screen retained an active upload view")
	}
}

func TestVerificationNeverLooksLikeCompletedUpload(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output}
	u.BeginUpload(UploadSummary{TotalBytes: 100 << 20})
	u.UploadProgress(100 << 20)
	for _, verifying := range []bool{false, true} {
		if verifying {
			u.VerifyUpload()
		}
		screen := strings.Join(u.uploadLines(80), "\n")
		for _, completedSignal := range []string{"100%", "━━", "Ctrl+C 暂停", "MiB/s", "剩余约"} {
			if strings.Contains(screen, completedSignal) {
				t.Fatalf("unfinished task displayed %q: %s", completedSignal, screen)
			}
		}
		if !strings.Contains(screen, "任务尚未完成，请保持终端开启") {
			t.Fatalf("unfinished task did not explain that the terminal must stay open: %s", screen)
		}
		if verifying && !strings.Contains(screen, "服务端校验中") {
			t.Fatalf("transfer and verification were not separate states: %s", screen)
		}
	}
	u.InterruptUpload()
	screen := strings.Join(u.uploadLines(80), "\n")
	if !strings.Contains(screen, "已中断") || strings.Contains(screen, "100%") || strings.Contains(screen, "请保持终端开启") {
		t.Fatalf("interrupted verification retained a completed or running status: %s", screen)
	}
}
