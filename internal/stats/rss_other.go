//go:build unix && !darwin

package stats

// maxRSSUnit is the unit of Rusage.Maxrss in bytes: kilobytes on Linux
// and the BSDs.
const maxRSSUnit = 1024
