//go:build unix
// +build unix

package file

import (
	"os"
	"syscall"
)

// crossesFilesystem reports whether info and parent are positively known to sit
// on different filesystems.
//
// Unknown is not a crossing: when either FileInfo carries no *syscall.Stat_t the
// check is skipped, which is exactly what the two inline type assertions this
// replaced did.
func crossesFilesystem(info, parent os.FileInfo) bool {
	a, aok := info.Sys().(*syscall.Stat_t)
	b, bok := parent.Sys().(*syscall.Stat_t)
	return aok && bok && a.Dev != b.Dev
}
