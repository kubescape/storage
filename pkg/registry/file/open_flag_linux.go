//go:build linux
// +build linux

package file

import "syscall"

// openFlagDirect contains the platform-specific flag for O_DIRECT on Linux.
//
// On non-Linux platforms this symbol should be provided by another file
// (for example `open_flag_other.go`) with a zero value so that Direct I/O
// isn't requested when running on macOS or other systems.
var openFlagDirect = syscall.O_DIRECT
