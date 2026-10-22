package main

import (
	"fmt"
	"io"

	"github.com/modulestf/facts/internal/facts"
)

// rules lists every implemented rule and its tier, one per line, sorted by id.
func rules(stdout io.Writer) int {
	for _, r := range facts.Rules {
		fmt.Fprintf(stdout, "%s\t%s\n", r.ID, r.Tier)
	}
	return 0
}
