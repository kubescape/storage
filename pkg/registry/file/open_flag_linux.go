//go:build linux
// +build linux

package file

import (
	"errors"
	"syscall"
)

// openFlagDirect contains the platform-specific flag for O_DIRECT on Linux.
//
// On non-Linux platforms this symbol should be provided by another file
// (for example `open_flag_other.go`) with a zero value so that Direct I/O
// isn't requested when running on macOS or other systems.
var openFlagDirect = syscall.O_DIRECT

// isDirectIOUnsupported reports whether err indicates that the filesystem does
// not support O_DIRECT (e.g. tmpfs, overlayfs, some network mounts).
func isDirectIOUnsupported(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.EINVAL || errno == syscall.EOPNOTSUPP
	}
	return false
}
