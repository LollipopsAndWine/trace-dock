//go:build windows

package uninstall

import (
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func scheduleDelete(path string) error {
	cmd := exec.Command(path, cleanupArg, path)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

func cleanupExecutable(path string) error {
	var last error
	for i := 0; i < 100; i++ {
		if err := os.Remove(path); err == nil || os.IsNotExist(err) {
			return nil
		} else {
			last = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return last
}
