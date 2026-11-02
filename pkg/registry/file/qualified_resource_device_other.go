//go:build !unix
// +build !unix

// This file provides the non-unix fallback for `crossesFilesystem` so that code
// can compile and unit tests can run on developer machines, the same reason
// `open_flag_other.go` exists. There is no portable st_dev to compare, and on
// these platforms a FileInfo carries no *syscall.Stat_t, so the inline type
// assertions this replaced would not have held either.
package file

import "os"

// crossesFilesystem always reports false: the filesystem a path sits on cannot
// be established portably, and an unknown device was never treated as a
// crossing.
func crossesFilesystem(_, _ os.FileInfo) bool { return false }
