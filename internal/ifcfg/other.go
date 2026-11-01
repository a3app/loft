//go:build !linux

package ifcfg

import (
	"errors"
	"net"
)

// Netlink is Linux-only. On other systems loft configures the TUN with that
// system's own tools (ifconfig/route on macOS, the service on Windows), so
// this stub only exists to keep the package building everywhere.
func Netlink() Backend { return noNetlink{} }

var errNotLinux = errors.New("ifcfg: netlink is Linux-only")

type noNetlink struct{}

func (noNetlink) Name() string                     { return "netlink" }
func (noNetlink) AddrAdd(string, *net.IPNet) error { return errNotLinux }
func (noNetlink) AddrDel(string, *net.IPNet) error { return errNotLinux }
func (noNetlink) LinkUp(string, int) error         { return errNotLinux }
func (noNetlink) RouteAdd(Route) error             { return errNotLinux }
func (noNetlink) RouteReplace(Route) error         { return errNotLinux }
func (noNetlink) RouteDel(Route) error             { return errNotLinux }
func (noNetlink) RuleAdd(Rule) error               { return errNotLinux }
func (noNetlink) RuleDel(Rule) error               { return errNotLinux }

// DefaultGatewayV4 is Linux-only, see netlink_linux.go.
func DefaultGatewayV4() (net.IP, string, error) { return nil, "", errNotLinux }

// PromoteSecondaries is Linux-only, see sysctl_linux.go.
func PromoteSecondaries(string) error { return errNotLinux }
