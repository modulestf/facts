//go:build !unix

package main

import "os/exec"

// ownProcessGroup does nothing here: the context kills the go command only.
func ownProcessGroup(*exec.Cmd) {}
