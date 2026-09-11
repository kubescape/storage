//go:build !race

package file

// raceDetectorEnabled reports whether the test binary runs under -race, for
// tests whose time bounds are measured on the plain build.
const raceDetectorEnabled = false
