package behaviour

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"
)

// evidenceDigest is the sha256, in hex, of the evidence behind a trigger's
// outstanding differences: the head and base commits, the sorted difference
// ids, the base and head observations of every case with an outstanding
// difference, and the sorted fixture hashes of the trigger's valid runs at
// those levels. It hashes canonical JSON: object keys sorted, no
// whitespace, numbers as written. A finding that explains the differences
// carries it, so the host can tell that the finding judged this evidence:
// the same ids over other observations give another digest. It is "" only
// if the evidence does not encode, which no record ReadLadder accepted
// reaches; the host then finds no finding that covers the set.
func evidenceDigest(l *Ladder, id string, ids []string, open []Change, pairs []casePair) string {
	type caseObs struct {
		Level string      `json:"level"`
		Case  string      `json:"case"`
		Base  Observation `json:"base"`
		Head  Observation `json:"head"`
	}
	want, levels := map[[2]string]bool{}, map[string]bool{}
	for _, c := range open {
		want[[2]string{c.Level, c.Case}] = true
		levels[c.Level] = true
	}
	observed := []caseObs{}
	for _, p := range pairs {
		if want[[2]string{p.level, p.id}] {
			observed = append(observed, caseObs{Level: p.level, Case: p.id, Base: p.base, Head: p.head})
		}
	}
	slices.SortFunc(observed, func(a, b caseObs) int { return cmp.Or(cmp.Compare(a.Level, b.Level), cmp.Compare(a.Case, b.Case)) })
	fixtures := []string{}
	for _, r := range validRuns(l, id, slices.Sorted(maps.Keys(levels))) {
		if r.FixtureSHA256 != nil {
			fixtures = append(fixtures, *r.FixtureSHA256)
		}
	}
	slices.Sort(fixtures)
	raw, err := json.Marshal(struct {
		HeadSHA      string    `json:"head_sha"`
		BaseSHA      string    `json:"base_sha"`
		Outstanding  []string  `json:"outstanding"`
		Observations []caseObs `json:"observations"`
		Fixtures     []string  `json:"fixture_sha256"`
	}{deref(l.HeadSHA), deref(l.BaseSHA), ids, observed, slices.Compact(fixtures)})
	if err != nil {
		return ""
	}
	// Decoded and encoded again, every object has its keys sorted, also
	// inside the values the record carries as written.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return ""
	}
	canon, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:])
}
