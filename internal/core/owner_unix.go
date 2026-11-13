//go:build !windows

package core

import (
	"os"
	"syscall"
)

func ownedByCurrentUser(p string) bool {
	st, e := os.Stat(p)
	if e != nil {
		return false
	}
	v, ok := st.Sys().(*syscall.Stat_t)
	return ok && v.Uid == uint32(os.Getuid())
}

func secureDirectory(p string) error { return os.Chmod(p, 0700) }
