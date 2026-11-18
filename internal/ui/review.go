package ui

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"golang.org/x/term"
)

// ConfirmReview keeps the upload summary and confirmation together. Details
// open in normal terminal scrollback so they can be read and copied freely.
func (u *UI) ConfirmReview(prompt string, summary, details []string) (bool, error) {
	if !u.TTY {
		return false, fmt.Errorf("此操作需要交互终端")
	}
	restore, err := u.rawMode()
	if err != nil {
		return false, err
	}
	defer restore()
	return u.confirmReview(prompt, summary, details)
}

func (u *UI) confirmReview(prompt string, summary, details []string) (bool, error) {
	choice := confirmDefaultYes
	inline, printed := false, false
	hadAlternate := u.leaveScreen != nil
	defer func() {
		if inline && hadAlternate {
			u.leaveScreen = u.enterAlternateScreen()
		}
	}()
	for {
		lines := append(u.reviewHeaderLines(), summary...)
		lines = append(lines, "", u.Style("1", "◆ "+prompt))
		for i, label := range []string{"否", "是"} {
			if i == choice {
				lines = append(lines, u.Style("1;36", "> "+label))
			} else {
				lines = append(lines, "  "+label)
			}
		}
		const help = "↑↓ 选择 · Enter 确认 · D 查看详情 · Ctrl+C 退出"
		lines = append(lines, "", u.Style("2", help))
		width, height := u.reviewTerminalSize()
		if !u.ANSI || reviewRows(lines, width) >= height {
			inline = true
		}
		if inline {
			// Short/narrow terminals retain the complete summary in scrollback;
			// moving the choice only redraws the last line, never the summary.
			if !printed {
				u.EndFlow()
				for _, line := range append(u.reviewHeaderLines(), summary...) {
					u.Println("%s", line)
				}
				u.Break()
				u.Println("◆ %s", prompt)
				u.Println("%s", help)
				printed = true
			}
			if u.ANSI {
				u.Printf("\r\x1b[2K")
			}
			if choice == 1 {
				u.Printf("  否    > 是")
			} else {
				u.Printf("> 否      是")
			}
			if !u.ANSI {
				u.Break()
			}
		} else {
			u.renderScreen(lines)
		}

		k, err := key(u.reader)
		if err != nil {
			return false, err
		}
		switch k {
		case "up", "down", "left", "right", "k", "j", " ":
			choice = (choice + 1) % 2
		case "d", "D":
			if inline {
				u.Break()
			}
			if err := u.showReviewDetails(details); err != nil {
				return false, err
			}
			printed = false
		case "enter":
			if inline {
				u.Break()
			}
			u.history = append(u.history, entry{label: prompt, value: []string{"否", "是"}[choice]})
			return choice == 1, nil
		case "cancel":
			if inline {
				u.Break()
			}
			return false, fmt.Errorf("用户取消")
		}
	}
}

func (u *UI) showReviewDetails(lines []string) error {
	hadAlternate := u.leaveScreen != nil
	u.EndFlow()
	defer func() {
		if hadAlternate {
			u.leaveScreen = u.enterAlternateScreen()
		}
	}()
	u.Break()
	u.Println("%s", u.Style("1", "上传内容明细"))
	for _, line := range lines {
		u.Println("%s", line)
	}
	u.Break()
	u.Println("可用终端滚动查看、拖选复制 · Enter 返回确认 · Ctrl+C 退出")
	for {
		k, err := key(u.reader)
		if err != nil {
			return err
		}
		switch k {
		case "enter", "d", "D":
			return nil
		case "cancel":
			return fmt.Errorf("用户取消")
		}
	}
}

func (u *UI) reviewHeaderLines() []string {
	var lines []string
	if u.heading != "" {
		lines = append(lines, u.Style("1;36", u.heading))
	}
	for _, item := range u.history {
		lines = append(lines, "✓ "+item.label+"  "+item.value)
	}
	if u.title != "" {
		lines = append(lines, "", u.Style("1;36", strings.TrimSpace(u.step+"  ◆ "+u.title)))
	}
	return lines
}

func (u *UI) reviewTerminalSize() (int, int) {
	if out, ok := u.Out.(*os.File); ok {
		if width, height, err := term.GetSize(int(out.Fd())); err == nil && width > 0 && height > 0 {
			return width, height
		}
	}
	return 80, 24
}

var reviewColor = regexp.MustCompile("\x1b\\[[0-9;]*m")

func reviewRows(lines []string, width int) int {
	width = max(1, width)
	rows := 0
	for _, line := range lines {
		rows += max(1, (displayWidth(reviewColor.ReplaceAllString(line, ""))+width-1)/width)
	}
	return rows
}
