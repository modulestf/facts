//go:build !linux

package gates

// maxSymlinks is MAXSYMLINKS of the C library Perl's Cwd realpath is built
// with: 32 on macOS, so 33 links resolve and 34 do not, as measured.
const maxSymlinks = 32
