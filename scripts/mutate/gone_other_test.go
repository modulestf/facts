//go:build !unix

package main

import "testing"

// assertGone checks nothing here: only unix runs the tests in a process
// group that the budget's end kills whole.
func assertGone(*testing.T, int) {}
