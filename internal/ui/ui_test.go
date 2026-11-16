package ui

import (
	"strings"
	"testing"

	"github.com/trace-dock/trace-dock/internal/core"
)

func TestStyleRespectsColorSetting(t *testing.T) {
	u := &UI{}
	plain := u.Style("1;32", "ready")
	if plain != "ready" || strings.Contains(plain, "\x1b[") {
		t.Fatalf("disabled color emitted escape sequence: %q", plain)
	}
	u.Color = true
	colored := u.Style("1;32", "ready")
	if colored != "\x1b[1;32mready\x1b[0m" {
		t.Fatalf("unexpected color output: %q", colored)
	}
}

func TestConfirmationDefaultsToYes(t *testing.T) {
	if confirmDefaultYes != 1 || confirmDefaultNo != 0 {
		t.Fatalf("confirmation defaults = (%d, %d), want yes=1 no=0", confirmDefaultYes, confirmDefaultNo)
	}
}

func TestProgressUsesSingleTTYLine(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, TTY: true, Color: false}
	u.BeginFlow("Trace Dock")
	u.Step("05/07", "扫描并打包")
	u.Info("已选会话：session-1", "Workspace：/work/project")
	u.Progress("upload 1/2")
	u.Progress("upload 2/2")
	u.ProgressDone()
	got := output.String()
	if !strings.Contains(got, "upload 1/2") || !strings.Contains(got, "upload 2/2") {
		t.Fatalf("progress should redraw the current screen: %q", got)
	}
	if !strings.Contains(got, "已选会话：session-1\nWorkspace：/work/project\nupload 2/2") {
		t.Fatalf("progress redraw should preserve selected session and workspace: %s", got)
	}
}

func TestProgressNamedKeepsIndependentSessionLines(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, TTY: true, Color: false}
	u.BeginFlow("Trace Dock")
	u.Step("05/07", "扫描并打包")
	u.ProgressNamed("workspace:a", "workspace a 1/2")
	u.ProgressNamed("workspace:b", "workspace b 2/2")
	if !strings.Contains(output.String(), "workspace a 1/2") || !strings.Contains(output.String(), "workspace b 2/2") {
		t.Fatalf("named progress lines were overwritten: %s", output.String())
	}
	u.ProgressNamed("workspace:a", "workspace a 2/2")
	if !strings.Contains(output.String(), "workspace b 2/2") || !strings.Contains(output.String(), "workspace a 2/2") {
		t.Fatalf("updating one progress line removed another: %s", output.String())
	}
}

func TestProgressLinesFitTerminalWidthAndHeight(t *testing.T) {
	lines := map[string]string{
		"trajectory":        "✓ 会话轨迹检查完成 100/100 · 已完成 100 个 · 最近：019fdb84-238c-7791-9515-2911a7120",
		"workspace":         "- 扫描 workspace 文件：/home/user/project/.venv/lib/python/site-packages/very/long/path/module.json · 已检查 2086 项",
		"workspace-summary": "✓ workspace 完成 12/12",
	}
	fitted := fitProgressLines(lines, 40, 2)
	if len(fitted) != 2 {
		t.Fatalf("progress lines exceed terminal height: %#v", fitted)
	}
	if _, ok := fitted["trajectory"]; !ok {
		t.Fatal("trajectory summary should be kept when terminal height is constrained")
	}
	for _, line := range fitted {
		if displayWidth(line) > 40 {
			t.Fatalf("progress line exceeds terminal width: %q (%d)", line, displayWidth(line))
		}
	}
}

func TestStepKeepsConfirmedChoicesAboveCurrentQuestion(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, TTY: true}
	u.BeginFlow("Trace Dock")
	u.Step("", "连接服务")
	u.Record("服务端", "https://collector.example.com")
	u.Record("邀请代码", "验证通过")
	u.Step("01/07", "选择采集工具")

	got := output.String()
	lastScreen := got[strings.LastIndex(got, "\x1b[H\x1b[2J"):]
	for _, want := range []string{"https://collector.example.com", "验证通过", "01/07", "选择采集工具"} {
		if !strings.Contains(lastScreen, want) {
			t.Errorf("current screen lost %q: %s", want, lastScreen)
		}
	}
	if strings.Contains(lastScreen, "连接服务") {
		t.Fatalf("previous question should be collapsed into a result: %s", lastScreen)
	}
}

func TestRawTerminalRenderingUsesCarriageReturnLineFeed(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, TTY: true, raw: true}
	u.BeginFlow("Trace Dock")
	u.Step("01/07", "选择采集工具")
	u.Record("服务端", "http://127.0.0.1:8080")

	got := output.String()
	for i := 0; i < len(got); i++ {
		if got[i] == '\n' && (i == 0 || got[i-1] != '\r') {
			t.Fatalf("raw terminal emitted bare line feed at byte %d: %q", i, got)
		}
	}
}

func TestWrapTextPreservesLongWorkspacePaths(t *testing.T) {
	path := "/home/houlinfeng/PyCharmMiscProject/代码仓库/trace-dock"
	wrapped := wrapText(path, 18)
	if strings.Join(wrapped, "") != path {
		t.Fatalf("wrapped path changed: %#v", wrapped)
	}
	for _, line := range wrapped {
		if displayWidth(line) > 18 {
			t.Fatalf("wrapped line exceeds display width: %q (%d)", line, displayWidth(line))
		}
	}
}

func TestPrintWrappedShowsCompleteWorkspaceValue(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, Color: false}
	path := "/home/houlinfeng/PyCharmMiscProject/very-long-project-directory/trace-dock"
	u.PrintWrapped("Workspace: ", path)
	got := strings.NewReplacer("\n", "", " ", "").Replace(output.String())
	if !strings.Contains(got, path) {
		t.Fatalf("wrapped output dropped part of the path: %q", output.String())
	}
}

func TestSessionRowsKeepWorkspaceOnOneLogicalLine(t *testing.T) {
	path := "/home/houlinfeng/PyCharmMiscProject/very-long-project-directory/trace-dock"
	session := &core.Session{
		Tool: "codex",
		ID:   "01a19be6-fb1d-7000-8000-000000000000",
		Workspace: core.Workspace{
			Raw:    path,
			Kind:   "project",
			Policy: "snapshot",
		},
	}
	for name, row := range map[string]string{
		"discovery": discoverySessionLine(session),
		"selection": selectionSessionLine(session, ">", " ", "valid"),
	} {
		if strings.ContainsAny(row, "\r\n") || !strings.Contains(row, path) {
			t.Errorf("%s row should contain the complete path on one logical line: %q", name, row)
		}
	}
}
