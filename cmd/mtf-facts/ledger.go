package main

import "io"

// ledger checks a module's provider coverage ledger. Not implemented yet: it exits 2.
func ledger(_ []string, _, stderr io.Writer) int { return notImplemented("ledger", stderr) }
