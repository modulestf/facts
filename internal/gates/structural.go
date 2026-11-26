package gates

import (
	"maps"
	"slices"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// The G2 rule reads a module's source from the text, as the Perl did: the
// first source = "..." in the block, in a comment, a nested object or a
// string included. Terraform reads the block's own source attribute. The
// structural read below parses the file as Terraform does and adds a G2 hit
// for a module block whose source the text read passed but which Terraform
// would read differently. It only adds hits: a block the text read already
// refused gets no second line, and a block whose two reads agree keeps its
// output.

// srcReads are the text read's results for the module blocks of one file,
// keyed by the offset of the block's keyword: the source it passed, or
// refused for a hit.
type srcReads struct {
	pass    map[int]string
	refused map[int]bool
}

func newSrcReads() *srcReads {
	return &srcReads{pass: map[int]string{}, refused: map[int]bool{}}
}

// structuralSources adds a G2 hit for each module block of raw that the
// text read did not refuse and Terraform would read otherwise: the file does
// not parse, the source attribute is missing or not a constant string, it is
// another string than the text read passed, or, for a block the text read
// did not see, it fails the G2 rule.
func (r *run) structuralSources(f, raw string, sr *srcReads) {
	file, diags := hclsyntax.ParseConfig([]byte(raw), f, hcl.InitialPos)
	body, ok := file.Body.(*hclsyntax.Body)
	if diags.HasErrors() || !ok {
		// Terraform would refuse the file, but the gates cannot confirm a
		// source they cannot parse, so every passed block is a hit.
		for _, at := range slices.Sorted(maps.Keys(sr.pass)) {
			r.hit("G2", f, raw, at)
		}
		return
	}
	for _, b := range body.Blocks {
		if b.Type != "module" {
			continue
		}
		at := b.TypeRange.Start.Byte
		if sr.refused[at] {
			continue
		}
		got, ok := constSource(b.Body)
		want, passed := sr.pass[at]
		if !passed {
			// A block the text read did not see, such as module"x"{: the
			// rule applies to the source Terraform reads.
			ok = ok && r.sourceOK(dirOf(f), got)
		}
		if !ok || passed && got != want {
			r.hit("G2", f, raw, at)
		}
	}
}

// sourceOK is the G2 rule on one module source: a local path that resolves
// inside the clone, or a source of the registry namespace, or in runner
// mode of github.com.
func (r *run) sourceOK(dir, s string) bool {
	if localSource(s) {
		rp, ok := realpath(dir + "/" + s)
		return ok && under(rp, r.root)
	}
	return moduleSource(s) || r.o.Runner && githubSource(s)
}

// constSource is the module source Terraform reads: the source attribute
// as a constant, known, non-null string.
func constSource(b *hclsyntax.Body) (string, bool) {
	a, ok := b.Attributes["source"]
	if !ok {
		return "", false
	}
	v, diags := a.Expr.Value(nil)
	if diags.HasErrors() || v.IsNull() || !v.IsKnown() || !v.Type().Equals(cty.String) {
		return "", false
	}
	return v.AsString(), true
}
