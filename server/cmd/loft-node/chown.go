package main

import (
	"os"
	"syscall"
)

// chownLike gives path the owner and group of like (nil: leave it).
func chownLike(path string, like os.FileInfo) {
	if like == nil {
		return
	}
	if st, ok := like.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(path, int(st.Uid), int(st.Gid))
	}
}
