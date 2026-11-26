package gates

// maxSymlinks is MAXSYMLINKS of the C library Perl's Cwd realpath is built
// with: 20 on Linux, so 21 links resolve and 22 do not, as measured with the
// plan runner's Perl.
const maxSymlinks = 20
