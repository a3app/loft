//go:build unix

package transport

import (
	"os"
	"syscall"
)

// reexec replaces this process with a new run of the same executable and
// arguments under env. On success it does not return.
func reexec(env []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, env)
}
