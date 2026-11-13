package ui

import (
	"bufio"
	"fmt"
	"github.com/trace-dock/trace-dock/internal/core"
	"golang.org/x/term"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

type UI struct {
	In     *os.File
	Out    io.Writer
	reader *bufio.Reader
	TTY    bool
}

func New() *UI {
	return &UI{In: os.Stdin, Out: os.Stdout, reader: bufio.NewReader(os.Stdin), TTY: term.IsTerminal(int(os.Stdin.Fd()))}
}
func (u *UI) Printf(f string, args ...any) { fmt.Fprintf(u.Out, f, args...) }
func (u *UI) Line(prompt, def string) (string, error) {
	u.Printf("%s", prompt)
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
	u.Printf("个人 token（隐藏输入）: ")
	b, e := term.ReadPassword(int(u.In.Fd()))
	u.Printf("\n")
	return strings.TrimSpace(string(b)), e
}
func (u *UI) Choose(prompt string, items []string, initial int) (int, error) {
	if !u.TTY {
		return 0, fmt.Errorf("此操作需要交互终端")
	}
	old, e := term.MakeRaw(int(u.In.Fd()))
	if e != nil {
		return 0, e
	}
	defer term.Restore(int(u.In.Fd()), old)
	idx := initial
	r := u.reader
	for {
		u.Printf("\r\x1b[2K%s  ", prompt)
		for i, s := range items {
			if i == idx {
				u.Printf("[%s] ", s)
			} else {
				u.Printf(" %s  ", s)
			}
		}
		k, e := key(r)
		if e != nil {
			return 0, e
		}
		switch k {
		case "up", "left", "k":
			idx = (idx + len(items) - 1) % len(items)
		case "down", "right", "j", " ":
			idx = (idx + 1) % len(items)
		case "enter":
			u.Printf("\r\x1b[2K✓ %s：%s\r\n", prompt, items[idx])
			return idx, nil
		case "cancel":
			return 0, fmt.Errorf("用户取消")
		}
	}
}
func (u *UI) Confirm(prompt string) (bool, error) {
	i, e := u.Choose(prompt, []string{"否", "是"}, 0)
	return i == 1, e
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
	u.Printf("发现 %d 个根会话，可选 %d 个；未知 schema 禁止采集。\n", len(sessions), len(eligible))
	old, e := term.MakeRaw(int(u.In.Fd()))
	if e != nil {
		return nil, "", e
	}
	defer term.Restore(int(u.In.Fd()), old)
	r := bufio.NewReader(u.In)
	selected := map[*core.Session]bool{}
	cursor := 0
	query := ""
	search := false
	detail := false
	lines := 0
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
		if lines > 0 {
			u.Printf("\x1b[%dA\r\x1b[J", lines)
		}
		lines = 0
		print := func(f string, a ...any) { u.Printf(f+"\r\n", a...); lines++ }
		print("↑↓ 移动 · 空格选择 · A 全选/取消 · / 搜索 · D 详情 · PgUp/PgDn 翻页 · Enter 确认")
		print("已选 %d/%d；搜索：%s%s", len(selected), len(eligible), clean(query), map[bool]string{true: "_", false: ""}[search])
		start := (cursor / 8) * 8
		end := start + 8
		if end > len(visible) {
			end = len(visible)
		}
		for i := start; i < end; i++ {
			s := visible[i]
			mark := " "
			if selected[s] {
				mark = "x"
			}
			arrow := " "
			if i == cursor {
				arrow = ">"
			}
			ended := "未知"
			if s.EndedAt != nil {
				ended = s.EndedAt.Format("2006-01-02 15:04")
			}
			print("%s [%s] %s %s %s [%s] 子agent:%d", arrow, mark, s.Tool, clip(clean(s.ID), 16), ended, s.Validity, s.SubagentCount())
			print("       %s · %s/%s", clip(clean(s.Workspace.Raw), 40), s.Workspace.Kind, s.Workspace.Policy)
		}
		if detail && len(visible) > 0 {
			s := visible[cursor]
			print("会话: %s", clean(s.ID))
			print("轨迹: %s", clean(s.Source))
			print("cwd: %s；%s；%s", clean(s.Workspace.Raw), s.Workspace.Policy, s.Workspace.Reason)
			print("schema: %s；可逆性: %s；大小: %d", s.Schema, s.Reversibility, s.Size)
		}
		k, e := key(r)
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
			cursor -= 8
			if cursor < 0 {
				cursor = 0
			}
		case "pgdown":
			cursor += 8
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
				u.Printf("\x1b[%dA\r\x1b[J✓ 已选 %d 个根会话，未选 %d 个\r\n", lines, len(out), len(sessions)-len(out))
				return out, mode, nil
			}
		}
	}
}
