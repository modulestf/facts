// Command mtf-facts reports deterministic facts about Terraform module trees.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "usage: mtf-facts <command>")
	os.Exit(2)
}
