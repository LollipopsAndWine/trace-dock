//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

func configureConsole() bool {
	// Use UTF-8 for Chinese labels and enable VT processing for modern
	// Windows Terminal and cmd.exe. Older consoles fall back to plain output.
	_ = windows.SetConsoleCP(65001)
	_ = windows.SetConsoleOutputCP(65001)
	handle := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	if err := windows.SetConsoleMode(handle, mode|windows.ENABLE_PROCESSED_OUTPUT|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return false
	}
	return true
}
