//go:build !unix

package transport

import "errors"

// reexec is not available here: start the program with
// GODEBUG=http2xconnect=1 in its environment instead. The node runs on
// Linux, so this only matters for builds of the package on other systems.
func reexec([]string) error {
	return errors.New("set GODEBUG=http2xconnect=1 to serve the h2 and l3 fronts")
}
