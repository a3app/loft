//go:build unix

package adm

// This file reads a file's owner on Unix systems. It is split out because
// syscall.Stat_t only exists there.

import (
	"os"
	"syscall"
)

// owner is the uid/gid of a stat result, to keep it across a rewrite.
func owner(st os.FileInfo) (uid, gid int, ok bool) {
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(s.Uid), int(s.Gid), true
}
