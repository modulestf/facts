// Package data holds the pinned tables the rules read, embedded in the
// binary so a run reads no file for them.
package data

import _ "embed"

// CoreFeatures is core-features.json: Terraform core language features and
// the release that introduced each. README.md has its row schema and sources.
//
//go:embed core-features.json
var CoreFeatures []byte
