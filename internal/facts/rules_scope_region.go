package facts

import (
	"bytes"
	"regexp"
)

// regionText is regionPattern without its anchors: a region literal written
// with no escape is a run of a file's bytes that it matches.
var regionText = regexp.MustCompile(`[a-z]{2,4}(-[a-z]{2,4})?-(north|south|east|west|central|northeast|northwest|southeast|southwest)-[0-9]+`)

// mayHoldRegion reports whether data can hold a literal regionLiterals
// keeps, so a file that cannot is not lexed. A literal with no backslash
// decodes to its own text, and only a backslash escape can fail to decode or
// write a region the bytes do not show.
func mayHoldRegion(data []byte) bool {
	return bytes.IndexByte(data, '\\') >= 0 || regionText.Match(data)
}
