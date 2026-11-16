package ui

import (
	"bufio"
	"context"
	"errors"
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

func TestPlainTerminalProgressDoesNotRepeatStaticScreen(t *testing.T) {
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
	if strings.Count(got, "Trace Dock") != 1 || strings.Count(got, "已选会话：session-1") != 1 || strings.Count(got, "Workspace：/work/project") != 1 {
		t.Fatalf("plain terminal redraw repeated static content: %s", got)
	}
	if !strings.Contains(got, "upload 1/2\nupload 2/2") {
		t.Fatalf("plain terminal should append only changing progress lines: %s", got)
	}
}

func TestPlainConsoleSpinnerShowsLoadingStatusImmediately(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, TTY: true, ANSI: false}
	stop := u.StartSpinner("正在扫描候选会话")
	stop()
	if got := output.String(); got != "正在扫描候选会话...\n" {
		t.Fatalf("plain console did not show an immediate loading status: %q", got)
	}
}

func TestPlainTerminalStartupKeepsHeadingStepAndPromptOrder(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, In: nil, reader: bufio.NewReader(strings.NewReader("\n")), TTY: true, ANSI: false, Color: false}
	u.BeginFlow("Trace Dock 0.1.4 · 会话与 workspace 采集")
	u.Step("", "连接服务与身份验证")
	u.Line("服务地址", "https://collector.example.com")
	got := output.String()
	heading := "Trace Dock 0.1.4 · 会话与 workspace 采集"
	step := "连接服务与身份验证"
	if strings.Count(got, heading) != 1 || strings.Count(got, step) != 1 {
		t.Fatalf("plain terminal repeated static startup text: %s", got)
	}
	if strings.Index(got, heading) > strings.Index(got, step) || strings.Index(got, step) > strings.Index(got, "服务地址") {
		t.Fatalf("startup text is out of order: %s", got)
	}
}

func TestLineEditorDeletesUTF8CharactersAndCancelsOnCtrlC(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, reader: bufio.NewReader(strings.NewReader("夫人\x7f\n"))}
	value, err := u.Line("服务地址", "")
	if err != nil || value != "夫" {
		t.Fatalf("UTF-8 backspace editing failed: value=%q err=%v", value, err)
	}
	u.reader = bufio.NewReader(strings.NewReader("\x03"))
	beforeCancel := output.Len()
	if _, err = u.Line("服务地址", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ctrl+C did not cancel line input: %v", err)
	}
	if !strings.HasSuffix(output.String()[beforeCancel:], "\n") {
		t.Fatalf("Ctrl+C left the shell prompt on the input line: %q", output.String()[beforeCancel:])
	}
}

func TestHiddenLineEditorCancelsOnCtrlC(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, In: nil, reader: bufio.NewReader(strings.NewReader("\x03")), TTY: true}
	if _, err := u.Token(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ctrl+C did not cancel token input: %v", err)
	}
	if !strings.HasSuffix(output.String(), "\n") {
		t.Fatalf("Ctrl+C left the shell prompt on the token input line: %q", output.String())
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
	u := &UI{Out: &output, TTY: true, ANSI: true}
	u.BeginFlow("Trace Dock")
	u.Step("", "连接服务")
	u.Record("服务端", "https://collector.example.com")
	u.Record("邀请代码", "验证通过")
	u.Step("01/07", "选择采集工具")

	got := output.String()
	lastScreen := got[strings.LastIndex(got, "\x1b[2J\x1b[H"):]
	for _, want := range []string{"https://collector.example.com", "验证通过", "01/07", "选择采集工具"} {
		if !strings.Contains(lastScreen, want) {
			t.Errorf("current screen lost %q: %s", want, lastScreen)
		}
	}
	if strings.Contains(lastScreen, "连接服务") {
		t.Fatalf("previous question should be collapsed into a result: %s", lastScreen)
	}
}

func TestInteractiveFallbackDoesNotEmitANSIWhenConsoleVTFails(t *testing.T) {
	var output strings.Builder
	u := &UI{Out: &output, TTY: true, ANSI: false, Color: false}
	u.BeginFlow("Trace Dock")
	u.Step("01/07", "选择采集工具")
	u.ProgressLines(map[string]string{"main": "扫描中"})
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("plain console fallback emitted ANSI escape codes: %q", output.String())
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

func TestWorkspaceLabelsExplainParentClassificationInChinese(t *testing.T) {
	session := &core.Session{Workspace: core.Workspace{
		Raw:    "/home/user/dev",
		Kind:   "parent",
		Policy: "path_only",
		Reason: "multiple_independent_projects",
	}}
	row := discoverySessionLine(session)
	if !strings.Contains(row, "父目录（包含多个项目）/仅记录路径") {
		t.Fatalf("workspace row should explain the parent category: %s", row)
	}
	if strings.Contains(row, "parent") || strings.Contains(row, "path_only") {
		t.Fatalf("workspace row exposed internal policy values: %s", row)
	}
	if got := workspaceReason(session); got != "包含多个独立项目" {
		t.Fatalf("workspace reason = %q", got)
	}
}

func TestInvalidTrajectoryLabelsIncludeReason(t *testing.T) {
	for reason, want := range map[string]string{
		"trajectory_parse_error":         "轨迹无效：解析异常",
		"completed_with_recorded_errors": "轨迹无效：有历史中断或报错",
		"error_or_interruption_recorded": "轨迹无效：中断或错误",
		"completion_not_observed":        "轨迹无效：未发现结束标记",
	} {
		session := &core.Session{Schema: "codex.payload.v2", Validity: "invalid", ValidityReason: reason}
		if got := sessionStatusLabel(session); got != want {
			t.Errorf("sessionStatusLabel(%q) = %q, want %q", reason, got, want)
		}
	}
}

func TestSelectionTableUsesAlignedColumnsAndCompactStatus(t *testing.T) {
	sessions := []*core.Session{
		{
			Tool: "codex", ID: "session-normal-123456", Validity: "valid", Size: 1536,
			Workspace: core.Workspace{Raw: "/work/project", Policy: "snapshot"},
		},
		{
			Tool: "claude", ID: "session-abnormal-123456", Validity: "invalid", ValidityReason: "completed_with_recorded_errors", Size: 4096,
			Workspace: core.Workspace{Raw: "/work/archive-only", Policy: "path_only"},
		},
	}
	rows := [][]string{
		selectionSessionCells(sessions[0], " ", selectionValidity(sessions[0])),
		selectionSessionCells(sessions[1], "✓", selectionValidity(sessions[1])),
	}
	widths := tableColumnWidths(selectionSessionHeaders, rows)
	header := formatAlignedRow(selectionSessionHeaders, widths)
	displaySlice := func(value string, start, width int) string {
		var out strings.Builder
		position := 0
		for _, r := range value {
			runeWidth := displayWidth(string(r))
			if position >= start && position < start+width {
				out.WriteRune(r)
			}
			position += runeWidth
		}
		return strings.TrimSpace(out.String())
	}

	for i := range sessions {
		row := formatAlignedRowWithLeftColumn(rows[i], widths, 5)
		offset := 0
		for column := range selectionSessionHeaders {
			headerCell := selectionSessionHeaders[column]
			rowCell := rows[i][column]
			if got := displaySlice(header, offset, widths[column]); got != headerCell {
				t.Errorf("header column %q rendered as %q", headerCell, got)
			}
			if got := displaySlice(row, offset, widths[column]); got != strings.TrimSpace(rowCell) {
				t.Errorf("row column %q rendered as %q", rowCell, got)
			}
			offset += widths[column] + 2
		}
	}

	if rows[0][6] != "snapshot" || rows[1][6] != "path_only" {
		t.Fatalf("selection table should show policy codes: %q, %q", rows[0][6], rows[1][6])
	}
	if rows[0][7] != "normal" || rows[1][7] != "abnormal" {
		t.Fatalf("selection table should use normal/abnormal statuses: %q, %q", rows[0][7], rows[1][7])
	}
	if strings.Contains(selectionSessionLine(sessions[1], " ", " ", selectionValidity(sessions[1])), "轨迹无效：") {
		t.Fatal("selection row should not include the verbose invalidity reason")
	}
}

func TestWorkspacePathValuesLeftAlignButHeaderStaysCentered(t *testing.T) {
	session := &core.Session{
		Tool: "claude", ID: "session-123456", Validity: "valid",
		Workspace: core.Workspace{Raw: "/workspace/project/with/a/longer/path", Policy: "snapshot"},
	}
	row := discoverySessionCells(session)
	widths := tableColumnWidths(discoverySessionHeaders, [][]string{row})
	header := formatAlignedRow(discoverySessionHeaders, widths)
	formattedRow := formatAlignedRowWithLeftColumn(row, widths, 4)
	offset := 0
	for column := 0; column < 4; column++ {
		offset += widths[column] + 2
	}
	pathStart := displayWidth(formattedRow[:strings.Index(formattedRow, session.Workspace.Raw)])
	if pathStart != offset {
		t.Fatalf("workspace path starts at %d, want left edge of column %d", pathStart, offset)
	}
	headerIndex := strings.Index(header, discoverySessionHeaders[4])
	headerStart := displayWidth(header[:headerIndex])
	wantHeaderStart := offset + (widths[4]-displayWidth(discoverySessionHeaders[4]))/2
	if headerStart != wantHeaderStart {
		t.Fatalf("workspace heading starts at %d, want centered start %d", headerStart, wantHeaderStart)
	}
}

func TestSelectionHeaderCentersOverCheckbox(t *testing.T) {
	session := &core.Session{Validity: "valid"}
	row := selectionSessionCells(session, " ", selectionValidity(session))
	widths := tableColumnWidths(selectionSessionHeaders, [][]string{row})
	header := "  " + formatAlignedRow(selectionSessionHeaders, widths)
	content := "> " + formatAlignedRow(row, widths)
	selectionStart := displayWidth(header[:strings.Index(header, "选择")])
	checkboxStart := displayWidth(content[:strings.Index(content, "[ ]")])
	selectionCenter := float64(selectionStart) + float64(displayWidth("选择"))/2
	checkboxCenter := float64(checkboxStart) + float64(displayWidth("[ ]"))/2
	if diff := selectionCenter - checkboxCenter; diff < -0.5 || diff > 0.5 {
		t.Fatalf("selection heading center %.1f does not align with checkbox center %.1f", selectionCenter, checkboxCenter)
	}
}

func TestSelectionTableBoundsAndEllipsizesWorkspaceColumn(t *testing.T) {
	session := &core.Session{
		Tool: "codex", ID: "01a1e38e-9aad-7000-8000-000000000000", Validity: "valid",
		Workspace: core.Workspace{
			Raw:    "/home/houlinfeng/PyCharmMiscProject/a/very/deeply/nested/path/for/a/long/workspace/trace-dock",
			Policy: "snapshot",
		},
	}
	rows := [][]string{selectionSessionCells(session, " ", selectionValidity(session))}
	limit := workspaceColumnLimit(selectionSessionHeaders, rows, 5, 120)
	clipped := clipDisplay(rows[0][5], limit)
	if limit > 44 || displayWidth(clipped) > limit {
		t.Fatalf("workspace column was not bounded: limit=%d value=%q width=%d", limit, clipped, displayWidth(clipped))
	}
	if !strings.Contains(clipped, "…") || !strings.HasSuffix(clipped, "trace-dock") {
		t.Fatalf("clipped workspace path should retain an ellipsis and its suffix: %q", clipped)
	}
	if workspacePath(session) == clipped {
		t.Fatal("table clipping must not replace the full workspace path used in details")
	}
}

func TestDiscoveryTableMatchesSelectionStatusAndPolicyColumns(t *testing.T) {
	session := &core.Session{
		Tool: "claude", ID: "session-abnormal-123456", Validity: "invalid", Size: 2048,
		Workspace: core.Workspace{Raw: "/work/archive-only", Policy: "path_only"},
	}
	row := discoverySessionCells(session)
	if len(discoverySessionHeaders) != len(row) || discoverySessionHeaders[len(discoverySessionHeaders)-1] != "轨迹状态" {
		t.Fatalf("discovery table headers do not include trajectory status: %q", discoverySessionHeaders)
	}
	if row[5] != "path_only" || row[6] != "abnormal" {
		t.Fatalf("discovery table should share policy/status values: %q", row)
	}
	if strings.Contains(formatAlignedRow(row, tableColumnWidths(discoverySessionHeaders, [][]string{row})), "轨迹无效：") {
		t.Fatalf("discovery table should use the compact status value: %q", row)
	}
}
