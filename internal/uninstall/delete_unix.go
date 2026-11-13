//go:build !windows

package uninstall

import "os"

func scheduleDelete(path string) error { return os.Remove(path) }
func cleanupExecutable(path string) error { return os.Remove(path) }
