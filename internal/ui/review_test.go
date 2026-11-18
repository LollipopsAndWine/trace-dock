package ui

import (
	"bufio"
	"strings"
	"testing"
)

func TestReviewConfirmsDirectlyWithoutPaging(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, TTY: true, ANSI: true, reader: bufio.NewReader(strings.NewReader("\r"))}
	yes, err := u.confirmReview("确认上传？", []string{"上传 2 / 已选 3 份", "预计总量 4454.05 MiB"}, []string{"完整明细"})
	if err != nil || !yes {
		t.Fatalf("direct confirmation: yes=%v err=%v", yes, err)
	}
	for _, text := range []string{"上传 2 / 已选 3 份", "4454.05 MiB", "确认上传？", "D 查看详情"} {
		if !strings.Contains(output.String(), text) {
			t.Errorf("summary missing %q: %s", text, output.String())
		}
	}
	if strings.Contains(output.String(), "PgUp") || strings.Contains(output.String(), "完整明细") {
		t.Fatal("summary should provide direct confirmation with optional details")
	}
}

func TestReviewIgnoresMouseWheelReportsButKeepsArrowKeys(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		yes   bool
	}{
		{"wheel only", "\x1b[<64;12;4M\x1b[<65;12;4M\r", true},
		{"wheel then arrow", "\x1b[<64;12;4M\x1b[A\r", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output strings.Builder
			u := &UI{Out: &output, TTY: true, ANSI: true, reader: bufio.NewReader(strings.NewReader(test.input))}
			yes, err := u.confirmReview("确认上传？", []string{"上传摘要"}, nil)
			if err != nil || yes != test.yes {
				t.Fatalf("choice=%v err=%v, want choice=%v", yes, err, test.yes)
			}
		})
	}
}

func TestReviewFitsSummaryAndConfirmationInNormalTerminal(t *testing.T) {
	var output strings.Builder
	u := &UI{
		Out: &output, TTY: true, ANSI: true, Color: true,
		reader:  bufio.NewReader(strings.NewReader("\r")),
		heading: "Trace Dock 0.1.0-dev · 会话与 workspace 采集",
		history: []entry{{label: "服务端", value: "http://127.0.0.1:8080"}, {label: "身份", value: "h***@example.com"}},
		step:    "05/07", title: "核对实际内容并最终确认上传",
	}
	u.leaveScreen = u.enterAlternateScreen()
	summary := []string{
		"主会话轨迹  上传 2 / 已选 3 份 · 1 份不上传",
		"项目快照    上传 3 份（按目录去重）",
		"各类清单    上传 5 份（含 manifest 和排除记录）",
		"预计总量    4454.05 MiB · 10 份文件",
		"体积分项    轨迹 397.0 KiB · 快照 4453.56 MiB · 清单 103.7 KiB",
		"文件排除    敏感信息 1 项 · 大文件 2 项 · 其他 10 项",
		"大文件      文件类型排除 1 项 · 体积超限 1 项，正文不上传",
		"轨迹未上传  敏感信息排除 1 份",
	}
	if yes, err := u.confirmReview("确认上传以上内容？", summary, nil); err != nil || !yes {
		t.Fatalf("confirmation: yes=%v err=%v", yes, err)
	}
	got := output.String()
	if strings.Contains(got, "\x1b[?1049l") {
		t.Fatal("normal 80x24 terminal should show the whole summary and choices together")
	}
	frame := got[strings.LastIndex(got, "\x1b[2J\x1b[H")+len("\x1b[2J\x1b[H"):]
	if rows := reviewRows(strings.Split(strings.TrimSuffix(frame, "\x1b[J"), "\n"), 80); rows > 24 {
		t.Fatalf("confirmation screen overflows terminal: %d rows", rows)
	}
}

func TestReviewDetailsDoNotChangeChoiceOrLoseContent(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, TTY: true, ANSI: true, reader: bufio.NewReader(strings.NewReader("\x1b[A" + "d\r\r"))}
	u.leaveScreen = u.enterAlternateScreen()
	details := []string{"会话 ID 019fc6b7-86df-7631-83ee-4afa12a58374", "项目快照仍上传", "详细清单：/jobs/job/manifest.json"}
	yes, err := u.confirmReview("确认上传？", []string{"上传摘要"}, details)
	if err != nil || yes {
		t.Fatalf("no choice should survive details: yes=%v err=%v", yes, err)
	}
	for _, line := range details {
		if !strings.Contains(output.String(), line) {
			t.Errorf("details lost %q", line)
		}
	}
	got := output.String()
	leave := strings.Index(got, "\x1b[?1049l")
	detail := strings.Index(got, details[0])
	if leave < 0 || leave > detail {
		t.Fatal("details must be printed in normal terminal scrollback")
	}
	if strings.Contains(got[leave:detail], "\x1b[3J") {
		t.Fatal("printing details cleared terminal scrollback")
	}
}

func TestTallReviewPreservesEverySummaryLineInScrollback(t *testing.T) {
	var output strings.Builder
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = strings.Repeat("重要统计", i+1)
	}
	u := &UI{Out: &output, TTY: true, ANSI: true, reader: bufio.NewReader(strings.NewReader("\r"))}
	u.leaveScreen = u.enterAlternateScreen()
	yes, err := u.confirmReview("确认上传？", lines, nil)
	if err != nil || !yes {
		t.Fatalf("tall summary confirmation: yes=%v err=%v", yes, err)
	}
	got := output.String()
	for _, line := range lines {
		if !strings.Contains(got, line) {
			t.Fatal("tall summary discarded a statistic")
		}
	}
	if strings.Contains(got, "折叠") || strings.Contains(got, "PgDn") {
		t.Fatal("tall summary must remain available without folding or paging")
	}
}
