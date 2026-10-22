package main

import "io"

// schemaDiff compares provider schemas for a provider upgrade. Not implemented yet: it exits 2.
func schemaDiff(_ []string, _, stderr io.Writer) int { return notImplemented("schema-diff", stderr) }
