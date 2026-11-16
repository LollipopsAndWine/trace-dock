package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/trace-dock/trace-dock/internal/core"
	"golang.org/x/term"
)

type UI struct {
	In         *os.File
	Out        io.Writer
	reader     *bufio.Reader
	TTY        bool
	Color      bool
	raw        bool
	heading    string
	step       string
	title      string
	history    []entry
	info       []string
	activity   string
	activities map[string]string
	upload     *uploadView
	result     *CollectionResult
}

type entry struct{ label, value string }

func New() *UI {
	tty := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	_, noColor := os.LookupEnv("NO_COLOR")
	return &UI{In: os.Stdin, Out: os.Stdout, reader: bufio.NewReader(os.Stdin), TTY: tty, Color: tty && !noColor && os.Getenv("TERM") != "dumb"}
}
func (u *UI) Printf(f string, args ...any)  { fmt.Fprintf(u.Out, f, args...) }
func (u *UI) Println(f string, args ...any) { u.Printf(f+u.lineEnd(), args...) }
func (u *UI) Break()                        { _, _ = fmt.Fprint(u.Out, u.lineEnd()) }
func WrapLines(prefix, value string) []string {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		width = 80
	}
	if width < 40 {
		width = 80
	}
	indent := strings.Repeat(" ", displayWidth(prefix))
	lines := wrapText(value, width-displayWidth(prefix))
	result := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			result = append(result, prefix+line)
		} else {
			result = append(result, indent+line)
		}
	}
	return result
}
func (u *UI) PrintWrapped(prefix, value string) {
	for _, line := range WrapLines(prefix, value) {
		u.Println("%s", line)
	}
}
func (u *UI) lineEnd() string {
	if u.raw {
		return "\r\n"
	}
	return "\n"
}
func (u *UI) rawMode() (func(), error) {
	state, err := term.MakeRaw(int(u.In.Fd()))
	if err != nil {
		return nil, err
	}
	u.raw = true
	return func() {
		_ = term.Restore(int(u.In.Fd()), state)
		u.raw = false
	}, nil
}
func displayWidth(s string) int {
	width := 0
	for _, r := range s {
		if isWideRune(r) {
			width += 2
		} else {
			width++
		}
	}
	return width
}

func wrapText(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	lines := []string{}
	var line strings.Builder
	used := 0
	for _, r := range s {
		if r == '\n' || (used > 0 && used+displayWidth(string(r)) > width) {
			lines = append(lines, line.String())
			line.Reset()
			used = 0
			if r == '\n' {
				continue
			}
		}
		line.WriteRune(r)
		used += displayWidth(string(r))
	}
	if line.Len() > 0 || len(lines) == 0 {
		lines = append(lines, line.String())
	}
	return lines
}

func isWideRune(r rune) bool {
	return r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		r >= 0x2e80 && r <= 0xa4cf && r != 0x303f ||
		r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff ||
		r >= 0xfe10 && r <= 0xfe19 || r >= 0xfe30 && r <= 0xfe6f ||
		r >= 0xff00 && r <= 0xff60 || r >= 0xffe0 && r <= 0xffe6 ||
		r >= 0x1f300 && r <= 0x1faff || r >= 0x20000 && r <= 0x3fffd)
}

func (u *UI) pageSize(maximum, fixedLines, rowLines int) int {
	_, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || height < 1 {
		return maximum
	}
	available := height - fixedLines - len(u.history) - len(u.info)
	if rowLines < 1 {
		rowLines = 1
	}
	count := available / rowLines
	if count < 1 {
		count = 1
	}
	return min(maximum, count)
}

func workspacePolicy(session *core.Session) string {
	policy := session.Workspace.Policy
	if policy == "" {
		policy = "仅路径"
	}
	return policy
}

func workspacePath(session *core.Session) string {
	if session.Workspace.Raw == "" {
		return "(未记录 workspace 路径)"
	}
	return clean(session.Workspace.Raw)
}

func discoverySessionLine(session *core.Session) string {
	return fmt.Sprintf("%s / %s · %s · %s · %d · %s · %s/%s",
		session.Tool, clip(clean(session.ID), 16), endedAt(session), humanBytes(session.Size),
		session.SubagentCount(), workspacePath(session), session.Workspace.Kind, workspacePolicy(session))
}

func selectionSessionLine(session *core.Session, arrow, mark, validity string) string {
	return fmt.Sprintf("%s [%s] %s / %s · %s · %s · %d · %s · %s/%s · %s",
		arrow, mark, session.Tool, clip(clean(session.ID), 16), endedAt(session), humanBytes(session.Size),
		session.SubagentCount(), workspacePath(session), session.Workspace.Kind, workspacePolicy(session), validity)
}

func sessionStatusLabel(session *core.Session) string {
	switch session.Schema {
	case "unknown":
		return "格式未知，可归档"
	case "codex.mixed":
		return "混合格式，可归档"
	}
	switch session.Validity {
	case "valid":
		return "轨迹有效"
	case "invalid":
		return "轨迹无效"
	default:
		return "轨迹状态未知"
	}
}

func sessionValidityExplanation(session *core.Session) string {
	switch session.ValidityReason {
	case "completion_observed":
		return "识别到结束标记，且未发现解析异常、中断或错误记录。"
	case "trajectory_parse_error":
		return "JSONL 记录解析失败或读取异常，保守计为无效；可读取的原文仍可归档。"
	case "completed_with_recorded_errors":
		return "存在完成标记，也记录过中断或报错；当前按整份日志保守计为无效，仍可归档。"
	case "error_or_interruption_recorded":
		return "日志记录了中断、错误、超时或输出截断，当前保守计为无效，仍可归档。"
	case "completion_not_observed":
		return "未识别到结束标记，可能尚未结束或结束字段未被识别；仍可归档。"
	default:
		return "该状态描述日志的完成与错误证据；不表示敏感信息检测结果。"
	}
}
func (u *UI) Style(code, text string) string {
	if !u.Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}
func (u *UI) BeginFlow(title string) {
	u.heading = title
	u.renderBase()
}
func (u *UI) Step(number, title string) {
	u.step, u.title = number, title
	u.info = nil
	u.activity = ""
	u.activities = nil
	u.upload = nil
	u.result = nil
	u.renderBase()
}
func (u *UI) Info(lines ...string) {
	u.info = append([]string(nil), lines...)
	u.renderBase()
}
func (u *UI) Record(label, value string) {
	u.history = append(u.history, entry{label: label, value: value})
	u.renderBase()
}

func (u *UI) CompactHistory(labels ...string) {
	keep := map[string]bool{}
	for _, label := range labels {
		keep[label] = true
	}
	compact := u.history[:0]
	for _, item := range u.history {
		if keep[item.label] {
			compact = append(compact, item)
		}
	}
	u.history = compact
}
func (u *UI) Success(text string) { u.Record("", text) }
func (u *UI) Muted(text string)   { u.Info(text) }
func (u *UI) Finish(lines ...string) {
	u.info = append([]string(nil), lines...)
	u.renderBase()
}
func (u *UI) renderBase() {
	if u.result != nil {
		u.renderResult()
		return
	}
	if u.upload != nil {
		u.renderUpload()
		return
	}
	if u.TTY {
		u.Printf("\x1b[H\x1b[2J")
	}
	if u.heading != "" {
		u.Println("%s", u.Style("1;36", u.heading))
	}
	for _, item := range u.history {
		if item.label == "" {
			u.Println("%s %s", u.Style("1;32", "✓"), item.value)
		} else {
			u.Println("%s %s  %s", u.Style("1;32", "✓"), u.Style("2", item.label), u.Style("36", item.value))
		}
	}
	if u.title != "" {
		prefix := "◆"
		if strings.Contains(u.step, "/") {
			prefix = u.step + "  ◆"
		}
		u.Break()
		u.Println("%s %s", u.Style("1;36", prefix), u.Style("1", u.title))
	}
	for _, line := range u.info {
		u.Println("%s", line)
	}
	activityLines := []string{}
	if len(u.activities) > 0 {
		keys := make([]string, 0, len(u.activities))
		for key := range u.activities {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			activityLines = append(activityLines, u.activities[key])
		}
	} else if u.activity != "" {
		activityLines = append(activityLines, u.activity)
	}
	for _, line := range activityLines {
		u.Println("%s", u.Style("36", line))
	}
}
func (u *UI) Progress(text string) {
	u.ProgressNamed("main", text)
}
func (u *UI) ProgressNamed(key, text string) {
	if u.TTY {
		if u.activities == nil {
			u.activities = map[string]string{}
		}
		u.activities[key] = text
		u.renderBase()
		return
	}
	u.Printf("%s\n", text)
}

func (u *UI) ProgressLines(lines map[string]string) {
	width, height := 80, 24
	if output, ok := u.Out.(*os.File); ok {
		if columns, rows, err := term.GetSize(int(output.Fd())); err == nil {
			if columns > 0 {
				width = columns
			}
			if rows > 0 {
				height = rows
			}
		}
	}
	capacity := 3
	if u.TTY {
		reserved := len(u.history) + len(u.info)
		if u.heading != "" {
			reserved++
		}
		if u.title != "" {
			reserved += 2
		}
		capacity = max(1, height-reserved-1)
	}
	fitted := fitProgressLines(lines, width, capacity)
	u.activities = fitted
	if u.TTY {
		u.renderBase()
		return
	}
	keys := make([]string, 0, len(fitted))
	for key := range fitted {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		u.Printf("%s\n", fitted[key])
	}
}

func fitProgressLines(lines map[string]string, width, capacity int) map[string]string {
	priority := []string{"trajectory", "workspace", "workspace-summary", "main"}
	keys := make([]string, 0, len(lines))
	for _, key := range priority {
		if _, ok := lines[key]; ok {
			keys = append(keys, key)
		}
	}
	others := make([]string, 0, len(lines)-len(keys))
	for key := range lines {
		found := false
		for _, selected := range keys {
			if key == selected {
				found = true
				break
			}
		}
		if !found {
			others = append(others, key)
		}
	}
	sort.Strings(others)
	keys = append(keys, others...)
	if capacity < 1 {
		capacity = 1
	}
	if len(keys) > capacity {
		keys = keys[:capacity]
	}
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = clipProgressLine(lines[key], width)
	}
	return result
}

func clipProgressLine(line string, width int) string {
	if width < 1 {
		return ""
	}
	if displayWidth(line) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}
	remaining := width - 1
	leftWidth := remaining / 2
	rightWidth := remaining - leftWidth
	var left, right strings.Builder
	used := 0
	for _, r := range line {
		rw := displayWidth(string(r))
		if used+rw > leftWidth {
			break
		}
		left.WriteRune(r)
		used += rw
	}
	used = 0
	runes := []rune(line)
	for i := len(runes) - 1; i >= 0; i-- {
		rw := displayWidth(string(runes[i]))
		if used+rw > rightWidth {
			break
		}
		right.WriteRune(runes[i])
		used += rw
	}
	reversed := []rune(right.String())
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return left.String() + "…" + string(reversed)
}
func (u *UI) ProgressDone() {
	u.activity = ""
	u.activities = nil
	if u.TTY {
		u.renderBase()
	}
}
func (u *UI) Line(prompt, def string) (string, error) {
	u.renderBase()
	u.Printf("%s", u.Style("1;36", "◆ "+prompt))
	if def != "" {
		u.Printf(" [%s]", def)
	}
	u.Printf(": ")
	s, e := u.reader.ReadString('\n')
	if e != nil {
		return "", e
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = def
	}
	return s, nil
}
func (u *UI) Token() (string, error) {
	if !u.TTY {
		return "", fmt.Errorf("需要交互终端输入 token")
	}
	u.renderBase()
	u.Printf("%s", u.Style("1;36", "◆ 邀请代码（隐藏输入）: "))
	b, e := term.ReadPassword(int(u.In.Fd()))
	u.Printf("\n")
	return strings.TrimSpace(string(b)), e
}
func (u *UI) Choose(prompt string, items []string, initial int) (int, error) {
	if !u.TTY {
		return 0, fmt.Errorf("此操作需要交互终端")
	}
	restore, e := u.rawMode()
	if e != nil {
		return 0, e
	}
	defer restore()
	idx := initial
	for {
		u.renderBase()
		u.Break()
		u.Println("%s", u.Style("1", "◆ "+prompt))
		for i, s := range items {
			if i == idx {
				u.Println("%s %s", u.Style("1;36", ">"), u.Style("1;32", s))
			} else {
				u.Println("  %s", s)
			}
		}
		u.Break()
		u.Println("%s", u.Style("2", "↑↓ 选择 · Enter 确认 · Ctrl+C 退出"))
		k, e := key(u.reader)
		if e != nil {
			return 0, e
		}
		switch k {
		case "up", "left", "k":
			idx = (idx + len(items) - 1) % len(items)
		case "down", "right", "j", " ":
			idx = (idx + 1) % len(items)
		case "enter":
			u.Record(prompt, items[idx])
			return idx, nil
		case "cancel":
			return 0, fmt.Errorf("用户取消")
		}
	}
}
func (u *UI) Confirm(prompt string) (bool, error) {
	return u.confirm(prompt, confirmDefaultYes)
}

func (u *UI) ConfirmNo(prompt string) (bool, error) {
	return u.confirm(prompt, confirmDefaultNo)
}

func (u *UI) confirm(prompt string, initial int) (bool, error) {
	i, e := u.Choose(prompt, []string{"否", "是"}, initial)
	return i == 1, e
}

const (
	confirmDefaultNo  = 0
	confirmDefaultYes = 1
)

func (u *UI) ChooseScope(sessions []*core.Session) (bool, error) {
	eligible := make([]*core.Session, 0, len(sessions))
	for _, session := range sessions {
		if session.Eligibility != "excluded" {
			eligible = append(eligible, session)
		}
	}
	if len(eligible) == 0 {
		return false, fmt.Errorf("没有可采集会话")
	}
	restore, err := u.rawMode()
	if err != nil {
		return false, err
	}
	defer restore()

	choice, cursor, query := 0, 0, ""
	search, detail := false, false
	for {
		visible := make([]*core.Session, 0, len(eligible))
		for _, session := range eligible {
			if strings.Contains(strings.ToLower(session.ID+" "+session.Workspace.Raw), strings.ToLower(query)) {
				visible = append(visible, session)
			}
		}
		width, _, sizeErr := term.GetSize(int(os.Stdout.Fd()))
		if sizeErr != nil || width < 40 {
			width = 80
		}
		rowLines := 1
		for _, session := range visible {
			rowLines = max(rowLines, (displayWidth(discoverySessionLine(session))+width-1)/width)
		}
		fixedLines := 15
		if detail {
			fixedLines += 9
		}
		if query != "" {
			fixedLines++
		}
		pageSize := u.pageSize(8, fixedLines, rowLines)
		pages := (len(visible) + pageSize - 1) / pageSize
		if pages == 0 {
			pages = 1
		}
		page := cursor / pageSize
		if page >= pages {
			page = pages - 1
			cursor = page * pageSize
		}

		u.renderBase()
		u.Break()
		u.Println("%s", u.Style("1", "◆ 发现清单"))
		u.Println("会话（工具 / ID） · 结束时间 · 轨迹大小 · 子 agent · Workspace 路径 · 处理方式")
		start := page * pageSize
		end := min(start+pageSize, len(visible))
		for i := start; i < end; i++ {
			u.Println("%s", discoverySessionLine(visible[i]))
		}
		if len(visible) == 0 {
			u.Println("%s", u.Style("33", "没有匹配的可采集会话。"))
		}
		u.Break()
		u.Println("%s", u.Style("2", fmt.Sprintf("第 %d/%d 页 · ↑↓ 选择范围 · PgUp/PgDn 浏览 · / 搜索 · D 查看详情", page+1, pages)))
		if query != "" {
			u.Println("搜索：%s（仅筛选预览；全部上传仍覆盖全部符合条件的会话）", clean(query))
		}
		if detail && len(visible) > 0 {
			session := visible[start]
			u.Break()
			u.Println("%s", u.Style("1;36", "当前会话详情"))
			u.Println("ID: %s · 结束时间: %s · 大小: %s", clean(session.ID), endedAt(session), humanBytes(session.Size))
			u.Println("轨迹: %s", clean(session.Source))
			u.Println("Workspace: %s · %s · %s", clean(session.Workspace.Raw), session.Workspace.Policy, session.Workspace.Reason)
			u.Println("格式: %s · 状态: %s · 子 agent: %d", session.Schema, sessionStatusLabel(session), session.SubagentCount())
			u.PrintWrapped("判定原因：", sessionValidityExplanation(session))
			u.PrintWrapped("", "敏感信息检测在授权采集后单独执行，与此状态无关。")
		}
		u.Break()
		u.Println("%s", u.Style("1", "◆ 本次上传哪些会话？"))
		options := []string{"全部上传", "选择部分"}
		for i, option := range options {
			if i == choice {
				u.Println("%s %s", u.Style("1;36", ">"), u.Style("1;32", option))
			} else {
				u.Println("  %s", option)
			}
		}
		u.Break()
		u.Println("%s", u.Style("2", "Enter 确认 · 全选仍遵守目录授权和密钥排除规则"))

		keyValue, keyErr := key(u.reader)
		if keyErr != nil {
			return false, keyErr
		}
		if search {
			switch keyValue {
			case "enter":
				search = false
			case "cancel":
				return false, fmt.Errorf("用户取消")
			case "backspace":
				_, n := utf8.DecodeLastRuneInString(query)
				if n > 0 {
					query = query[:len(query)-n]
				}
			default:
				if len(keyValue) == 1 {
					query += keyValue
				}
			}
			cursor = 0
			continue
		}
		switch keyValue {
		case "up", "k":
			choice = (choice + len(options) - 1) % len(options)
		case "down", "j":
			choice = (choice + 1) % len(options)
		case "pgup":
			if cursor >= pageSize {
				cursor -= pageSize
			}
		case "pgdown":
			if cursor+pageSize < len(visible) {
				cursor += pageSize
			}
		case "/":
			search = true
		case "d", "D":
			detail = !detail
		case "cancel":
			return false, fmt.Errorf("用户取消")
		case "enter":
			if choice == 0 {
				u.Record("上传范围", fmt.Sprintf("全部 %d 个符合条件的主会话", len(eligible)))
				return false, nil
			}
			u.Record("上传范围", "选择部分会话")
			return true, nil
		}
	}
}

func key(r *bufio.Reader) (string, error) {
	b, e := r.ReadByte()
	if e != nil {
		return "", e
	}
	switch b {
	case 3, 4:
		return "cancel", nil
	case 13, 10:
		return "enter", nil
	case 127, 8:
		return "backspace", nil
	case 27:
		b, e = r.ReadByte()
		if e != nil {
			return "", e
		}
		if b != '[' {
			return "", nil
		}
		b, e = r.ReadByte()
		if e != nil {
			return "", e
		}
		switch b {
		case 'A':
			return "up", nil
		case 'B':
			return "down", nil
		case 'C':
			return "right", nil
		case 'D':
			return "left", nil
		case '5', '6':
			_, _ = r.ReadByte()
			if b == '5' {
				return "pgup", nil
			}
			return "pgdown", nil
		}
		return "", nil
	default:
		return string([]byte{b}), nil
	}
}
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 32 && r != 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func clip(s string, limit int) string {
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit-1]) + "…"
	}
	return s
}
func (u *UI) Select(sessions []*core.Session) ([]*core.Session, string, error) {
	eligible := []*core.Session{}
	for _, s := range sessions {
		if s.Eligibility != "excluded" {
			eligible = append(eligible, s)
		}
	}
	if len(eligible) == 0 {
		return nil, "", fmt.Errorf("没有可采集会话")
	}
	restore, e := u.rawMode()
	if e != nil {
		return nil, "", e
	}
	defer restore()
	selected := map[*core.Session]bool{}
	cursor := 0
	query := ""
	search := false
	detail := false
	for {
		visible := []*core.Session{}
		for _, s := range eligible {
			if strings.Contains(strings.ToLower(s.ID+" "+s.Workspace.Raw), strings.ToLower(query)) {
				visible = append(visible, s)
			}
		}
		if cursor >= len(visible) {
			cursor = len(visible) - 1
		}
		if cursor < 0 {
			cursor = 0
		}
		u.renderBase()
		searchLabel := clean(query)
		if search {
			searchLabel += "_"
		}
		status := fmt.Sprintf("已选 %d/%d", len(selected), len(eligible))
		if search || query != "" {
			status += " · 搜索：" + searchLabel
		}
		u.Break()
		u.Println("%s", u.Style("1", "◆ 选择要上传的会话"))
		u.Println("%s", u.Style("2", status))
		u.Println("选择 · 会话（工具 / ID） · 结束时间 · 轨迹大小 · 子 agent · Workspace 路径 · 处理方式")
		width, _, sizeErr := term.GetSize(int(os.Stdout.Fd()))
		if sizeErr != nil || width < 40 {
			width = 80
		}
		help := "↑↓ 移动 · 空格选择 · A 全选/取消 · / 搜索 · D 详情 · PgUp/PgDn 翻页 · Enter 确认"
		if search {
			help = "输入关键词筛选 · Backspace 删除 · Enter 结束搜索 · Ctrl+C 退出"
		}
		helpLines := wrapText(help, width)
		rowLines := 1
		for _, session := range visible {
			rowLines = max(rowLines, (displayWidth(selectionSessionLine(session, ">", "✓", sessionStatusLabel(session)))+width-1)/width)
		}
		fixedLines := 10 + len(helpLines)
		if detail {
			fixedLines += 9
		}
		pageSize := u.pageSize(8, fixedLines, rowLines)
		pages := max(1, (len(visible)+pageSize-1)/pageSize)
		page := cursor/pageSize + 1
		start := (cursor / pageSize) * pageSize
		end := start + pageSize
		if end > len(visible) {
			end = len(visible)
		}
		for i := start; i < end; i++ {
			s := visible[i]
			mark := " "
			if selected[s] {
				mark = u.Style("1;32", "✓")
			}
			arrow := " "
			if i == cursor {
				arrow = u.Style("1;36", ">")
			}
			validity := sessionStatusLabel(s)
			if s.Validity == "valid" && s.Schema != "unknown" && s.Schema != "codex.mixed" {
				validity = u.Style("32", validity)
			} else {
				validity = u.Style("33", validity)
			}
			u.Println("%s", selectionSessionLine(s, arrow, mark, validity))
		}
		if detail && len(visible) > 0 {
			s := visible[cursor]
			u.Break()
			u.Println("%s", u.Style("1;36", "当前会话详情"))
			u.Println("ID: %s · 结束时间: %s · 大小: %s", clean(s.ID), endedAt(s), humanBytes(s.Size))
			u.Println("轨迹: %s", clean(s.Source))
			u.Println("Workspace: %s · %s · %s", clean(s.Workspace.Raw), s.Workspace.Policy, s.Workspace.Reason)
			u.Println("格式: %s · 状态: %s · 子 agent: %d", s.Schema, sessionStatusLabel(s), s.SubagentCount())
			u.PrintWrapped("判定原因：", sessionValidityExplanation(s))
			u.PrintWrapped("", "敏感信息检测在授权采集后单独执行，与此状态无关。")
		}
		u.Break()
		u.Println("%s", u.Style("2", fmt.Sprintf("第 %d/%d 页", page, pages)))
		for _, line := range helpLines {
			u.Println("%s", u.Style("2", line))
		}
		k, e := key(u.reader)
		if e != nil {
			return nil, "", e
		}
		if search {
			switch k {
			case "enter":
				search = false
			case "cancel":
				return nil, "", fmt.Errorf("用户取消")
			case "backspace":
				_, n := utf8.DecodeLastRuneInString(query)
				if n > 0 {
					query = query[:len(query)-n]
				}
			default:
				if len(k) == 1 {
					query += k
				}
			}
			cursor = 0
			continue
		}
		switch k {
		case "up", "k":
			if cursor > 0 {
				cursor--
			}
		case "down", "j":
			if cursor+1 < len(visible) {
				cursor++
			}
		case "pgup":
			cursor -= pageSize
			if cursor < 0 {
				cursor = 0
			}
		case "pgdown":
			cursor += pageSize
			if cursor >= len(visible) {
				cursor = len(visible) - 1
			}
		case " ":
			if len(visible) > 0 {
				s := visible[cursor]
				if selected[s] {
					delete(selected, s)
				} else {
					selected[s] = true
				}
			}
		case "a", "A":
			if len(selected) == len(eligible) {
				selected = map[*core.Session]bool{}
			} else {
				for _, s := range eligible {
					selected[s] = true
				}
			}
		case "/":
			search = true
		case "d", "D":
			detail = !detail
		case "cancel":
			return nil, "", fmt.Errorf("用户取消")
		case "enter":
			if len(selected) > 0 {
				out := []*core.Session{}
				for _, s := range eligible {
					if selected[s] {
						out = append(out, s)
					}
				}
				mode := "partial"
				if len(out) == len(eligible) {
					mode = "all"
				}
				label := "部分选择"
				if mode == "all" {
					label = "全部符合条件的会话"
				}
				u.Record("选择范围", fmt.Sprintf("%s（%d/%d 个根会话）", label, len(out), len(eligible)))
				return out, mode, nil
			}
		}
	}
}

func endedAt(s *core.Session) string {
	if s.EndedAt == nil {
		return "未知"
	}
	return s.EndedAt.Format("2006-01-02 15:04")
}

func humanBytes(size int64) string {
	if size >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(size)/(1<<20))
	}
	return fmt.Sprintf("%.1f KiB", float64(size)/1024)
}
