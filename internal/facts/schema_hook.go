package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/modulestf/facts/internal/schema"
)

// applySchema loads the schema handoff and the raw schema files named in o,
// binds the handoff to the run's commits and .tf tree hashes, and gives every
// area the result. A missing, oversized or malformed input is an error. A
// handoff for other commits or trees is no error: its lookups are
// schema_unavailable. It returns the envelope's schema_inputs, nil when the
// run read no schema input, with nil handoff keys when it read raw files only.
func applySchema(o Options, head, base *Tree, areas []*Area) (*SchemaInputs, error) {
	if o.SchemaHandoff == "" && len(o.SchemaFiles) == 0 {
		return nil, nil
	}
	s, err := schema.Load(o.SchemaHandoff, o.SchemaFiles)
	if err != nil {
		return nil, err
	}
	r := schema.Run{HeadSHA: o.HeadSHA, MergeBaseSHA: o.MergeBaseSHA, HeadTreeHash: tfTreeHash(head)}
	if base != nil {
		r.BaseTreeHash = tfTreeHash(base)
	}
	s.Bind(r)
	for _, a := range areas {
		a.Schema = s
	}
	in := &SchemaInputs{SchemaFiles: []FileHash{}}
	for _, f := range s.SchemaFiles {
		in.SchemaFiles = append(in.SchemaFiles, FileHash{Path: f.Path, SHA256: f.SHA256})
	}
	if h := s.Handoff; h != nil {
		sum, headSHA, mergeBaseSHA := s.HandoffSHA256, h.HeadSHA, h.MergeBaseSHA // copies: the envelope does not alias the set
		in.HandoffSHA256, in.HeadSHA, in.MergeBaseSHA = &sum, &headSHA, &mergeBaseSHA
		in.TreeHashes = &TreeHashes{Head: h.Inputs.Head.TreeHash, MergeBase: h.Inputs.MergeBase.TreeHash}
	}
	return in, nil
}

// tfTreeHash is the handoff's tree_hash of t: SHA-256 of one
// "<sha256>  <path>\n" line per regular .tf file, sorted by path bytes.
// Both sides leave symbolic links out and count only regular .tf files: the
// gate keeps only blobs of mode 100644 or 100755, and the hosted extraction
// never writes a link. An oversize file is left out, as the gate leaves out a
// blob over 1 MiB; an unreadable one has no hash, so its tree does not match.
func tfTreeHash(t *Tree) string {
	h := sha256.New()
	for _, p := range t.Paths { // sorted by bytes
		if e := t.Entries[p]; e.Kind == KindFile && strings.HasSuffix(p, ".tf") {
			fmt.Fprintf(h, "%s  %s\n", e.SHA256, p)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
