package facts

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// ChangesSchemaVersion is the version of changes.schema.json.
const ChangesSchemaVersion = 1

// File statuses of a changes report.
const (
	FileAdded    = "added"
	FileRemoved  = "removed"
	FileModified = "modified"
)

// Why a changed file was not parsed. An entry kind other than KindFile,
// such as KindSymlink or KindNoPatch, is its own reason.
const (
	ChangeReasonJSON      = "json"              // a .tf.json file
	ChangeReasonOverride  = "override"          // override.tf or *_override.tf: it merges into blocks of other files
	ChangeReasonParse     = "parse-error"       // the HCL does not parse
	ChangeReasonLimit     = "limit"             // the file is over a parser limit
	ChangeReasonDuplicate = "duplicate-address" // two resource, data, module, variable or output blocks of one file share an address
	ChangeReasonTofu      = "tofu"              // a .tofu or .tofu.json file, which OpenTofu reads in place of a .tf file of its name
)

// uniqueTypes are the block types Terraform allows once per address in a
// module. Every other type, locals, moved, import, removed, terraform, check
// and provider among them, may repeat, and its blocks are matched in order.
var uniqueTypes = map[string]bool{"resource": true, "data": true, "module": true, "variable": true, "output": true}

// metaArgs are the meta-arguments of a resource or data block. A change of
// one changes which instances exist, how they are configured or in what
// order, not one value: it is a structural edit, never a changed attribute.
var metaArgs = map[string]bool{"count": true, "for_each": true, "provider": true, "depends_on": true}

// Read states of a changed attribute.
const (
	ReadNone    = "unread"  // no expression of the module reads it
	ReadFound   = "read"    // an expression of the module reads it
	ReadUnknown = "unknown" // the module has a file the scan cannot read, or the block is not a resource or data block
)

// How a reader reads a changed attribute.
const (
	HowName    = "name"     // by the attribute's name after the address
	HowObject  = "object"   // the whole object, or an index or splat whose key is not known
	HowSelf    = "self"     // self.<name> inside the block
	HowTagsAll = "tags_all" // tags_all, which the provider derives from tags
	HowUnknown = "unknown"  // a file of the module the scan cannot read
)

// maxReaders bounds the readers listed per attribute; the read state does
// not depend on it.
const maxReaders = 32

// ChangesOptions are the inputs of Changes. Exactly one of Base and Diff is
// set.
type ChangesOptions struct {
	Head   string // directory of the head tree
	Base   string // directory of the tree the change started from
	Diff   string // GitHub pull request files list or unified diff; see ReadDiffBase
	Limits Limits
}

// ChangesReport is what a change does to the HCL of a module tree: per
// changed configuration file its top-level blocks and their changed direct
// attributes, whether the module reads each changed attribute, and the data
// source types each head directory declares.
type ChangesReport struct {
	SchemaVersion  int              `json:"schema_version"`
	HeadTreeSHA256 string           `json:"head_tree_sha256"`
	Base           ChangesBase      `json:"base"`
	Files          []ChangedFile    `json:"files"`
	DataSources    []DirDataSources `json:"data_sources"`
}

// ChangesBase says where the base tree came from.
type ChangesBase struct {
	Kind       string     `json:"kind"` // "tree" or "diff"
	TreeSHA256 *string    `json:"tree_sha256"`
	Diff       *DiffInput `json:"diff"`
}

// ChangedFile is one .tf or .tf.json path whose entry differs between base
// and head. Outside is set when anything changed besides the values of
// direct attributes of blocks present at both sides: a comment or blank
// line between blocks, a block added, removed or moved, or the file added,
// removed or not parsed.
type ChangedFile struct {
	Path    string         `json:"path"`
	Dir     string         `json:"dir"`
	Status  string         `json:"status"`
	Parsed  bool           `json:"parsed"`
	Reason  *string        `json:"reason"`
	Outside bool           `json:"outside"`
	Blocks  []ChangedBlock `json:"blocks"`
}

// ChangedBlock is one top-level block of a changed file that was added,
// removed or edited. Structural is set for an edit besides the values of
// its direct attributes: its header, a comment, an attribute added, removed
// or moved, or any nested block.
type ChangedBlock struct {
	Address    string             `json:"address"`
	Kind       string             `json:"kind"`
	Status     string             `json:"status"`
	Structural bool               `json:"structural"`
	Attributes []ChangedAttribute `json:"attributes"`
}

// ChangedAttribute is one direct attribute of an edited block, present at
// both sides, whose value changed.
type ChangedAttribute struct {
	Name             string     `json:"name"`
	Plain            PlainSides `json:"plain"`
	Read             string     `json:"read"`
	Readers          []Reader   `json:"readers"`
	ReadersTruncated bool       `json:"readers_truncated"`
}

// PlainSides says whether the attribute's value is a plain expression at
// each side; see plainExpr.
type PlainSides struct {
	Head bool `json:"head"`
	Base bool `json:"base"`
}

// Reader is one place that reads a changed attribute. Line is 0 for a file
// the scan cannot read.
type Reader struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	How  string `json:"how"`
}

// Changes reads the head tree and the change and reports what it does to
// the HCL; see ChangesReport.
func Changes(o ChangesOptions) (*ChangesReport, error) {
	if (o.Base == "") == (o.Diff == "") {
		return nil, errors.New("exactly one of a base tree and a diff is needed")
	}
	head, err := ReadTree(o.Head, o.Limits)
	if err != nil {
		return nil, fmt.Errorf("head tree: %w", err)
	}
	r := &ChangesReport{SchemaVersion: ChangesSchemaVersion, HeadTreeSHA256: head.TreeSHA256(),
		Files: []ChangedFile{}, DataSources: []DirDataSources{}}
	var base *Tree
	if o.Base != "" {
		if base, err = ReadTree(o.Base, o.Limits); err != nil {
			return nil, fmt.Errorf("base tree: %w", err)
		}
		sum := base.TreeSHA256()
		r.Base = ChangesBase{Kind: "tree", TreeSHA256: &sum}
	} else {
		var in *DiffInput
		if base, in, err = ReadDiffBase(head, o.Diff, o.Limits); err != nil {
			return nil, err
		}
		r.Base = ChangesBase{Kind: "diff", Diff: in}
	}
	for _, p := range changedPaths(head, base) {
		if err := o.Limits.expired(); err != nil {
			return nil, err
		}
		if !configPath(p) {
			continue
		}
		r.Files = append(r.Files, diffFile(p, head.Entries[p], base.Entries[p], o.Limits))
	}
	if err := markReads(r.Files, head, o.Limits); err != nil {
		return nil, err
	}
	if r.DataSources, err = dataSources(head, o.Limits); err != nil {
		return nil, err
	}
	return r, nil
}

// configPath reports a configuration file Terraform or OpenTofu reads and
// does not skip: .tf, .tf.json, .tofu or .tofu.json.
func configPath(p string) bool {
	name := path.Base(p)
	if ignoredName(name) {
		return false
	}
	for _, ext := range []string{".tf", ".tf.json", ".tofu", ".tofu.json"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// hclName reports a configuration file the engine parses: a .tf file. A
// JSON or OpenTofu file is never parsed.
func hclName(name string) bool { return strings.HasSuffix(name, ".tf") }

// overrideName reports an override file, which merges into blocks other
// files declare.
func overrideName(name string) bool {
	return name == "override.tf" || strings.HasSuffix(name, "_override.tf")
}

// parsedFile is one side of a changed file, parsed.
type parsedFile struct {
	src    []byte
	blocks []*hclsyntax.Block
	keys   []string // blockKeys of blocks
}

// parseSide parses one side of a changed file, or returns why it cannot.
func parseSide(p string, e *Entry, lim Limits) (*parsedFile, string) {
	name := path.Base(p)
	switch {
	case strings.HasSuffix(name, ".tofu") || strings.HasSuffix(name, ".tofu.json"):
		return nil, ChangeReasonTofu
	case strings.HasSuffix(name, ".tf.json"):
		return nil, ChangeReasonJSON
	case e.Kind != KindFile:
		return nil, e.Kind
	case overrideName(name):
		return nil, ChangeReasonOverride
	}
	body, _, err := parseSyntax(name, e.Data, lim)
	if err != nil {
		if errors.As(err, new(limitError)) {
			return nil, ChangeReasonLimit
		}
		return nil, ChangeReasonParse
	}
	blocks := slices.Clone(body.Blocks)
	keys, dup := blockKeys(blocks)
	if dup {
		return nil, ChangeReasonDuplicate
	}
	return &parsedFile{src: e.Data, blocks: blocks, keys: keys}, ""
}

// blockKeys identifies each top-level block of a file: its type and labels,
// and for a type that may repeat its place among the blocks of that type
// and labels. dup is set when a block of a uniqueTypes type repeats.
func blockKeys(blocks []*hclsyntax.Block) (keys []string, dup bool) {
	seen := map[string]int{}
	keys = make([]string, len(blocks))
	for i, b := range blocks {
		k := strings.Join(append([]string{b.Type}, b.Labels...), "\x00")
		n := seen[k]
		seen[k]++
		if n > 0 && uniqueTypes[b.Type] {
			return nil, true
		}
		keys[i] = fmt.Sprintf("%s\x00#%d", k, n)
	}
	return keys, false
}

// diffFile compares one changed configuration file. h or b is nil when the
// file is only at the other side.
func diffFile(p string, h, b *Entry, lim Limits) ChangedFile {
	f := ChangedFile{Path: p, Dir: path.Dir(p), Status: FileModified, Parsed: true, Blocks: []ChangedBlock{}}
	switch {
	case b == nil:
		f.Status = FileAdded
	case h == nil:
		f.Status = FileRemoved
	}
	var hp, bp *parsedFile
	for _, side := range []struct {
		e   *Entry
		out **parsedFile
	}{{h, &hp}, {b, &bp}} {
		if side.e == nil {
			continue
		}
		pf, why := parseSide(p, side.e, lim)
		if why != "" && f.Parsed {
			f.Parsed, f.Reason = false, &why
		}
		*side.out = pf
	}
	if !f.Parsed {
		f.Outside = true
		return f
	}
	f.Outside = f.Status != FileModified
	f.Blocks, f.Outside = diffBlocks(hp, bp, f.Outside)
	return f
}

// diffBlocks compares the top-level blocks of two sides, either nil when
// the file is absent there. It returns the added, removed and edited blocks
// in head order, then the removed ones in base order, and whether anything
// outside the blocks present at both sides changed.
func diffBlocks(hp, bp *parsedFile, outside bool) ([]ChangedBlock, bool) {
	var hb, bb []*hclsyntax.Block
	var hk, bk []string
	if hp != nil {
		hb, hk = hp.blocks, hp.keys
	}
	if bp != nil {
		bb, bk = bp.blocks, bp.keys
	}
	baseBy := map[string]*hclsyntax.Block{}
	for i, b := range bb {
		baseBy[bk[i]] = b
	}
	headKeys := map[string]bool{}
	out := []ChangedBlock{}
	var shared []string // keys at both sides, in head order
	for i, h := range hb {
		k := hk[i]
		headKeys[k] = true
		b, ok := baseBy[k]
		if !ok {
			out = append(out, ChangedBlock{Address: blockAddress(h), Kind: h.Type, Status: BlockAdded, Attributes: []ChangedAttribute{}})
			outside = true
			continue
		}
		shared = append(shared, k)
		if cb, edited := diffBlock(h, hp.src, b, bp.src); edited {
			out = append(out, cb)
		}
	}
	for i, b := range bb {
		if !headKeys[bk[i]] {
			out = append(out, ChangedBlock{Address: blockAddress(b), Kind: b.Type, Status: BlockRemoved, Attributes: []ChangedAttribute{}})
			outside = true
		}
	}
	if hp != nil && bp != nil && !outside {
		var baseShared []string
		for _, k := range bk {
			if headKeys[k] {
				baseShared = append(baseShared, k)
			}
		}
		outside = !slices.Equal(shared, baseShared) ||
			!slices.EqualFunc(gaps(hp.src, blockRanges(hb)), gaps(bp.src, blockRanges(bb)), bytes.Equal)
	}
	return out, outside
}

// diffBlock compares one block at both sides. edited is false when its
// bytes are equal.
func diffBlock(h *hclsyntax.Block, hsrc []byte, b *hclsyntax.Block, bsrc []byte) (ChangedBlock, bool) {
	hr, br := h.Range(), b.Range()
	if bytes.Equal(hr.SliceBytes(hsrc), br.SliceBytes(bsrc)) {
		return ChangedBlock{}, false
	}
	cb := ChangedBlock{Address: blockAddress(h), Kind: h.Type, Status: BlockEdited, Attributes: []ChangedAttribute{}}
	ha, ba := sortedAttrs(h.Body), sortedAttrs(b.Body)
	hv, bv := valueRanges(ha), valueRanges(ba)
	cb.Structural = !slices.EqualFunc(gapsIn(hsrc, hr, hv), gapsIn(bsrc, br, bv), bytes.Equal)
	baseAttr := map[string]*hclsyntax.Attribute{}
	for _, a := range ba {
		baseAttr[a.Name] = a
	}
	for _, a := range ha {
		o, ok := baseAttr[a.Name]
		if !ok {
			continue
		}
		hval, bval := a.Expr.Range().SliceBytes(hsrc), o.Expr.Range().SliceBytes(bsrc)
		if bytes.Equal(hval, bval) {
			continue
		}
		if refBlock(h) && metaArgs[a.Name] {
			cb.Structural = true
			continue
		}
		cb.Attributes = append(cb.Attributes, ChangedAttribute{Name: a.Name, Read: ReadUnknown, Readers: []Reader{},
			Plain: PlainSides{Head: plainExpr(a.Expr, hsrc), Base: plainExpr(o.Expr, bsrc)}})
	}
	return cb, true
}

// byteRange is a half-open range of source bytes.
type byteRange struct{ start, end int }

func blockRanges(bs []*hclsyntax.Block) []byteRange {
	out := make([]byteRange, 0, len(bs))
	for _, b := range bs {
		r := b.Range()
		out = append(out, byteRange{r.Start.Byte, r.End.Byte})
	}
	return out
}

func valueRanges(as []*hclsyntax.Attribute) []byteRange {
	out := make([]byteRange, 0, len(as))
	for _, a := range as {
		r := a.Expr.Range()
		out = append(out, byteRange{r.Start.Byte, r.End.Byte})
	}
	return out
}

// gaps returns the bytes of src outside the sorted, disjoint ranges: before
// the first, between each two and after the last.
func gaps(src []byte, rs []byteRange) [][]byte {
	out := make([][]byte, 0, len(rs)+1)
	at := 0
	for _, r := range rs {
		if r.start < at || r.end > len(src) || r.start > r.end {
			return [][]byte{nil, src} // not expected from the parser; never equal to a real split
		}
		out = append(out, src[at:r.start])
		at = r.end
	}
	return append(out, src[at:])
}

// gapsIn is gaps within the block range r, so bytes outside the block do
// not count.
func gapsIn(src []byte, r hcl.Range, rs []byteRange) [][]byte {
	if r.Start.Byte < 0 || r.End.Byte > len(src) || r.Start.Byte > r.End.Byte {
		return [][]byte{nil, src}
	}
	shifted := make([]byteRange, len(rs))
	for i, x := range rs {
		shifted[i] = byteRange{x.start - r.Start.Byte, x.end - r.Start.Byte}
	}
	return gaps(src[r.Start.Byte:r.End.Byte], shifted)
}

// blockAddress is how a report names a top-level block: <type>.<name> for a
// resource, data.<type>.<name>, module.<name>, var.<name>, output.<name>,
// provider.<name>, and the type and labels joined by dots for any other.
func blockAddress(b *hclsyntax.Block) string {
	l := b.Labels
	switch {
	case b.Type == "resource" && len(l) == 2:
		return l[0] + "." + l[1]
	case b.Type == "data" && len(l) == 2:
		return "data." + l[0] + "." + l[1]
	case b.Type == "variable" && len(l) == 1:
		return "var." + l[0]
	}
	return strings.Join(append([]string{b.Type}, l...), ".")
}
