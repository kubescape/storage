//go:build !linux
// +build !linux

// This file provides a fallback definition for `openFlagDirect` on non-Linux
// platforms (for example macOS) so that code can compile and unit tests can
// run on developer machines. On Linux the real `O_DIRECT` flag is provided in
// `open_flag_linux.go`.
//
// We intentionally use a zero value here so that OpenFile calls don't request
// direct I/O on platforms that don't support `O_DIRECT`.
package file

var openFlagDirect = 0
