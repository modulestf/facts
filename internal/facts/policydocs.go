package facts

// The iam-policy-docs report: the IAM policy documents a module tree
// defines, their normalized content, and the blocks that use each one.
// Everything is read, never evaluated. What the reader cannot decide is
// reported as undecidable with a reason code, never as equal or absent.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// PolicyDocsSchemaVersion is the version of iam-policy-docs.schema.json.
const PolicyDocsSchemaVersion = 1

// Kinds of policy document.
const (
	PolicyKindDataSource = "data_source" // a data "aws_iam_policy_document" block
	PolicyKindJSONEncode = "jsonencode"  // a jsonencode() call of an object with a Statement key
	PolicyKindJSONString = "json_string" // a string or heredoc that holds a JSON policy
	PolicyKindExpression = "expression"  // a policy argument whose value no document explains
)

// Reason codes of an undecidable directory, document content or use list.
// The directory codes are those of the lint manifest.
const (
	PolicyReasonDirectory        = "directory"             // the document's directory, or one its uses reach, is undecidable
	PolicyReasonDuplicate        = "duplicate"             // two documents of one address in one directory
	PolicyReasonDynamicBlock     = "dynamic_block"         // a dynamic block
	PolicyReasonMerge            = "merge"                 // source or override documents: the provider merges them, the reader does not
	PolicyReasonUnsupportedArg   = "unsupported_argument"  // an argument the reader has no model of
	PolicyReasonUnsupportedBlock = "unsupported_block"     // a nested block the reader has no model of
	PolicyReasonNotLiteral       = "not_literal"           // a value that is not a literal, a list of literals or a plain reference
	PolicyReasonInvalidEffect    = "invalid_effect"        // an effect other than Allow or Deny
	PolicyReasonInterpolatedJSON = "interpolated_json"     // a JSON string with an interpolation or directive
	PolicyReasonInvalidJSON      = "invalid_json"          // a JSON string that does not parse, or repeats a key
	PolicyReasonUnsupportedKey   = "unsupported_key"       // a JSON policy key the reader has no model of
	PolicyReasonUnresolved       = "unresolved_expression" // a policy argument whose value no document explains
	PolicyReasonTooLarge         = "too_large"             // over a statement, value or use limit
	PolicyReasonConditional      = "conditional"           // the value passes a conditional
	PolicyReasonFunction         = "function"              // the value passes a function other than concat, flatten, compact or tolist
	PolicyReasonForExpression    = "for_expression"        // the value passes a for expression
	PolicyReasonIndex            = "index"                 // an index, splat or attribute into a value the reader cannot follow
	PolicyReasonTemplate         = "template"              // the value is interpolated into a string
	PolicyReasonEmbedded         = "embedded"              // the value is read inside another policy document
	PolicyReasonShape            = "shape"                 // the value reaches an argument at a list depth the argument does not take
	PolicyReasonExternalModule   = "external_module"       // the value is passed to a module outside the tree
	PolicyReasonCallerUnknown    = "caller_unknown"        // the value leaves through an output and an undecidable directory may call it
	PolicyReasonOtherExpression  = "other_expression"      // any other expression
	PolicyReasonVariableDefault  = "variable_default"      // the document is a variable default: every reader of the variable reads it
)

// PolicyDocsOptions are the inputs of PolicyDocs.
type PolicyDocsOptions struct {
	Head   string // directory of the head tree
	Base   string // optional directory of the tree the change started from
	Limits Limits
}

// PolicyDocsReport is the iam-policy-docs report. Base and Compare are null
// when no base tree was given.
type PolicyDocsReport struct {
	SchemaVersion int           `json:"schema_version"`
	Head          *PolicyReport `json:"head"`
	Base          *PolicyReport `json:"base"`
	Compare       []PolicyPair  `json:"compare"`
	// Precondition says whether both trees pass the comparison precondition;
	// null without a base. When it is false every pair is undecidable, and
	// a tree whose only directories are undecidable has no pair at all: a
	// host reads it, never an empty compare, with head and base directories.
	Precondition *bool `json:"precondition"`
}

// PolicyReport lists the module directories of one tree and the documents
// in them, both in path order.
type PolicyReport struct {
	Directories []PolicyDir `json:"directories"`
	Documents   []PolicyDoc `json:"documents"`
}

// PolicyDir is one directory that holds Terraform configuration.
type PolicyDir struct {
	Dir         string        `json:"dir"`
	Undecidable *PolicyReason `json:"undecidable"`
}

// PolicyReason says why something is undecidable: a code from the closed
// set above and a detail for a person.
type PolicyReason struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// PolicyDoc is one policy document. Exactly one of Content and
// ContentUndecidable is set, and exactly one of Uses and UsesUndecidable.
type PolicyDoc struct {
	Dir                string        `json:"dir"`
	Address            string        `json:"address"`
	Kind               string        `json:"kind"`
	File               string        `json:"file"`
	Lines              [2]int        `json:"lines"`
	Gate               *PolicyGate   `json:"gate"`
	Instances          string        `json:"instances"`
	Content            *Policy       `json:"content"`
	ContentUndecidable *PolicyReason `json:"content_undecidable"`
	Uses               []PolicyUse   `json:"uses"`
	UsesUndecidable    *PolicyReason `json:"uses_undecidable"`

	contentKey string // hash of the content and of every definition it reads; "" when undecidable
	usesKey    string // hash of the uses and of every definition on their paths; "" when undecidable
}

// PolicyGate is a count or for_each meta-argument as written.
type PolicyGate struct {
	Dir     string `json:"dir,omitempty"`
	Address string `json:"address,omitempty"`
	Meta    string `json:"meta"`
	Text    string `json:"text"`
}

// Instance counts of a document or a use, decided from literal gates only.
const (
	InstancesNone        = "none"        // a gate is a literal 0 or an empty literal collection
	InstancesAlways      = "always"      // no gate, or every gate a literal non-zero count or non-empty collection
	InstancesConditional = "conditional" // any other gate
)

// PolicyUse is one block that reads a document: the block, the argument and
// every hop on the way there, with the count and for_each of each block on
// the way.
type PolicyUse struct {
	Dir       string       `json:"dir"`
	Address   string       `json:"address"`
	Argument  string       `json:"argument"`
	Via       []PolicyHop  `json:"via"`
	Gates     []PolicyGate `json:"gates"`
	Instances string       `json:"instances"`
}

// PolicyHop is one step between a document and the block that uses it.
type PolicyHop struct {
	Dir  string `json:"dir"`
	Node string `json:"node"`
	Role string `json:"role"`
}

// Roles of a hop.
const (
	HopLocal        = "local"         // a local value
	HopSource       = "source"        // a source_policy_documents entry of a merging document
	HopOverride     = "override"      // an override_policy_documents entry of a merging document
	HopModuleInput  = "module_input"  // an argument of a module call in the tree
	HopVariable     = "variable"      // the variable of the called module that receives it
	HopOutput       = "output"        // an output of a module called in the tree
	HopModuleOutput = "module_output" // the parent's read of that output
)

// PolicyPair compares the head and the base document of one directory and
// address. Content and Uses are null unless Presence is both.
type PolicyPair struct {
	Dir      string  `json:"dir"`
	Address  string  `json:"address"`
	Presence string  `json:"presence"`
	Content  *string `json:"content"`
	Uses     *string `json:"uses"`
}

// Values of PolicyPair.
const (
	PresenceBoth        = "both"
	PresenceHeadOnly    = "head_only"
	PresenceBaseOnly    = "base_only"
	PresenceUndecidable = "undecidable"
	CompareEqual        = "equal"
	CompareDiffers      = "differs"
	CompareUndecidable  = "undecidable"
)

// Bounds of one report beyond the tree limits.
const (
	maxPolicyStatements = 1000 // statements of one document
	maxPolicyValues     = 4000 // values of one list
	maxPolicyDefs       = 2000 // definitions one fingerprint closure reads
	maxPolicyHops       = 64   // hops of one use
)

// Bounds a test lowers.
var (
	maxPolicyUses    = 256     // uses of one document
	maxPolicySteps   = 20000   // trace steps of one document
	maxPolicyClosure = 1 << 20 // closure entries kept for one tree; past it the run is an error
)

// errPolicyClosure stops a run whose closures pass maxPolicyClosure.
var errPolicyClosure = errors.New("policy closures over the entry limit")

// PolicyDocs reads the head tree, and the base tree when given, and reports
// the policy documents of each and, with a base, how each pair compares.
func PolicyDocs(o PolicyDocsOptions) (*PolicyDocsReport, error) {
	head, err := ReadTree(o.Head, o.Limits)
	if err != nil {
		return nil, fmt.Errorf("head tree: %w", err)
	}
	r := &PolicyDocsReport{SchemaVersion: PolicyDocsSchemaVersion}
	hs, err := policyScan(head, o.Limits)
	if err != nil {
		return nil, err
	}
	r.Head = hs.report
	if o.Base == "" {
		return r, nil
	}
	base, err := ReadTree(o.Base, o.Limits)
	if err != nil {
		return nil, fmt.Errorf("base tree: %w", err)
	}
	bs, err := policyScan(base, o.Limits)
	if err != nil {
		return nil, err
	}
	r.Base = bs.report
	decided := hs.treeOK && bs.treeOK && hs.env == bs.env
	r.Compare, r.Precondition = policyCompare(hs, bs, decided), &decided
	return r, nil
}

// policyTree is one tree read for the report.
type policyTree struct {
	dirs     map[string]*policyDirInfo
	anyUndec bool // some directory is undecidable
	// treeOK is the precondition of every comparison: no link, special or
	// unreadable entry, no variables file of any name, no undecidable
	// directory, and every local module source resolved in the tree.
	treeOK bool
	// env hashes the provider and terraform blocks and the lock files of the
	// tree: an equal comparison needs the same on both sides.
	env string
	// entries counts the closure entries kept, against maxPolicyClosure.
	entries  int
	callers  map[string][]policyCall // child directory -> the calls of it
	report   *PolicyReport
	docIndex map[[2]string][]int // (dir, address) -> indexes into report.Documents
	lim      Limits
	fps      map[[32]byte][32]byte   // fingerprint cache: hash of file, range and form -> fingerprint
	defsMemo map[[32]byte][]defEntry // closure cache: hash of directory and references -> sorted entries, nil when undecided
}

// policyCall is one module call with a local source in the tree.
type policyCall struct {
	parent string
	name   string
	block  *hclsyntax.Block
	src    []byte
	file   string
}

// policyDirInfo is one parsed directory.
type policyDirInfo struct {
	dir         string
	undecidable *PolicyReason
	tfvars      bool // a variables file Terraform loads when the directory is a root module
	blocks      []policyBlock
	locals      map[string]*policyLocalAttr
	byAddr      map[string][]*policyBlock // block address -> blocks
	sites       []policySite
	docs        []*policyDocInfo
	byNode      map[string][]siteItem // node -> the flow items that name it
}

type policyBlock struct {
	addr  string
	file  string
	src   []byte
	block *hclsyntax.Block
}

type policyLocalAttr struct {
	file string
	src  []byte
	attr *hclsyntax.Attribute
}

// policyDocInfo is a document while the tree is scanned.
type policyDocInfo struct {
	doc      PolicyDoc
	key      string       // the node a flow names for it
	gate     *PolicyGate  // its own count or for_each
	refs     []string     // the references its content holds
	block    *policyBlock // a data source's block
	sinkDefs string       // an expression document's gate definitions
}

// policyScan reads every configuration directory of a tree.
func policyScan(t *Tree, lim Limits) (*policyTree, error) {
	pt := &policyTree{treeOK: true, dirs: map[string]*policyDirInfo{}, callers: map[string][]policyCall{},
		report: &PolicyReport{Directories: []PolicyDir{}, Documents: []PolicyDoc{}}, docIndex: map[[2]string][]int{}, lim: lim, fps: map[[32]byte][32]byte{}, defsMemo: map[[32]byte][]defEntry{}}
	for _, d := range policyConfigDirs(t) {
		if err := lim.expired(); err != nil {
			return nil, err
		}
		di := policyLoadDir(t, d, lim)
		pt.dirs[d] = di
		if di.undecidable != nil {
			pt.anyUndec = true
		}
		pt.report.Directories = append(pt.report.Directories, PolicyDir{Dir: d, Undecidable: di.undecidable})
	}
	pt.treeOK = !pt.anyUndec && policyTreeClean(t)
	var env []string
	for _, d := range slices.Sorted(maps.Keys(pt.dirs)) {
		di := pt.dirs[d]
		for _, b := range di.blocks {
			if b.block.Type != "module" || len(b.block.Labels) != 1 {
				continue
			}
			child, ok := policyLocalSource(d, b)
			switch {
			case ok && pt.dirs[child] != nil:
				pt.callers[child] = append(pt.callers[child], policyCall{parent: d, name: b.block.Labels[0], block: b.block, src: b.src, file: b.file})
			case ok, policyLocalLooking(b):
				pt.treeOK = false // a local source the tree does not hold
			}
		}
	}
	// The provider and terraform blocks and what they read, such as an
	// assume_role role_arn set from a local, once every call is known.
	for _, d := range slices.Sorted(maps.Keys(pt.dirs)) {
		di := pt.dirs[d]
		for i := range di.blocks {
			if b := &di.blocks[i]; b.block.Type == "provider" || b.block.Type == "terraform" {
				bd := pt.blockDefs(di, b)
				if bd == undecidedDef {
					pt.treeOK = false
				}
				env = append(env, bd)
			}
		}
	}
	for _, p := range t.Paths {
		if path.Base(p) == ".terraform.lock.hcl" {
			env = append(env, p+"="+t.Entries[p].SHA256)
		}
	}
	slices.Sort(env)
	pt.env = hashKey(env...)
	dirs := slices.Sorted(maps.Keys(pt.dirs))
	for _, d := range dirs {
		pt.scanDocs(pt.dirs[d])
	}
	for _, d := range dirs {
		di := pt.dirs[d]
		for _, doc := range di.docs {
			if err := lim.expired(); err != nil {
				return nil, err
			}
			pt.finishDoc(di, doc)
			pt.report.Documents = append(pt.report.Documents, doc.doc)
		}
	}
	// A trace the deadline cut short says so in a reason; the report is
	// then dropped, never written short.
	if err := lim.expired(); err != nil {
		return nil, err
	}
	if pt.entries > maxPolicyClosure {
		return nil, errPolicyClosure
	}
	for i := range pt.report.Documents {
		k := [2]string{pt.report.Documents[i].Dir, pt.report.Documents[i].Address}
		pt.docIndex[k] = append(pt.docIndex[k], i)
	}
	return pt, nil
}

// policyTreeClean reports a tree whose every entry is a regular file read
// whole (no link, special, unreadable or oversize entry, whose content the
// reader does not hold) and with no variables file of any name, hidden ones
// included: Terraform reads variables files a static reader does not tie to
// a run.
func policyTreeClean(t *Tree) bool {
	for _, p := range t.Paths {
		switch e, name := t.Entries[p], path.Base(p); {
		case e.Kind != KindFile:
			return false
		case strings.HasSuffix(name, ".tfvars") || strings.HasSuffix(name, ".tfvars.json"):
			return false
		}
	}
	return true
}

// policyLocalLooking reports a module call whose source is a local path,
// written as one or not: such a source must resolve in the tree.
func policyLocalLooking(b policyBlock) bool {
	a, ok := b.block.Body.Attributes["source"]
	if !ok {
		return true // no source: the configuration does not load
	}
	src, ok := literalString(a.Expr)
	if !ok {
		return true // a source that is not a literal
	}
	src = strings.ReplaceAll(src, `\`, "/")
	return strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") || strings.HasPrefix(src, "/") || src == "." || src == ".."
}

// policyConfigDirs lists, in path order, the directories that hold a file
// Terraform or OpenTofu would load as configuration.
func policyConfigDirs(t *Tree) []string {
	set := map[string]bool{}
	for _, p := range t.Paths {
		name := path.Base(p)
		if ignoredName(name) {
			continue
		}
		for _, ext := range []string{".tf", ".tf.json", ".tofu", ".tofu.json"} {
			if strings.HasSuffix(name, ext) {
				set[path.Dir(p)] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// policyLoadDir parses the configuration files of one directory with the
// file rules of ParseDir. An OpenTofu file also makes it undecidable: the
// report must hold for both tools.
func policyLoadDir(t *Tree, dir string, lim Limits) *policyDirInfo {
	di := &policyDirInfo{dir: dir, locals: map[string]*policyLocalAttr{},
		byAddr: map[string][]*policyBlock{}}
	undec := func(code, detail string) {
		if di.undecidable == nil {
			di.undecidable = &PolicyReason{Code: code, Detail: detail}
		}
	}
	for _, e := range t.DirEntries(dir) {
		name := path.Base(e.Path)
		if ignoredName(name) {
			continue
		}
		if strings.HasSuffix(name, ".tofu") || strings.HasSuffix(name, ".tofu.json") {
			undec(ReasonUnsupportedFile, name+" is an OpenTofu file")
			continue
		}
		if strings.HasSuffix(name, ".tfvars") || strings.HasSuffix(name, ".tfvars.json") {
			di.tfvars = true
			continue
		}
		if r := unsupportedName(name); r != "" {
			undec(ReasonUnsupportedFile, r)
			continue
		}
		if !strings.HasSuffix(name, ".tf") {
			continue
		}
		if e.Kind != KindFile {
			undec(kindCode(e.Kind), fmt.Sprintf("%s is %s", name, kindText(e.Kind)))
			continue
		}
		body, _, err := parseSyntax(name, e.Data, lim)
		if err != nil {
			code := ReasonParseError
			if IsLimitError(err) {
				code = ReasonLimitExceeded
			}
			undec(code, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		for _, b := range body.Blocks {
			pb := policyBlock{addr: policyBlockAddr(b), file: name, src: e.Data, block: b}
			di.blocks = append(di.blocks, pb)
		}
	}
	for i := range di.blocks {
		pb := &di.blocks[i]
		b := pb.block
		if b.Type == "locals" {
			for _, a := range sortedAttrs(b.Body) {
				if di.locals[a.Name] != nil {
					undec(ReasonParseError, "local."+a.Name+" is defined twice")
				}
				di.locals[a.Name] = &policyLocalAttr{file: pb.file, src: pb.src, attr: a}
			}
			continue
		}
		di.byAddr[pb.addr] = append(di.byAddr[pb.addr], pb)
	}
	return di
}

// policyBlockAddr is the address of a top-level block as references write
// it: T.N for a resource, data.T.N, module.N, var.N, output.N, provider.N,
// and the type and labels joined by dots for any other block.
func policyBlockAddr(b *hclsyntax.Block) string {
	switch {
	case b.Type == "resource" && len(b.Labels) == 2:
		return b.Labels[0] + "." + b.Labels[1]
	case b.Type == "data" && len(b.Labels) == 2:
		return "data." + b.Labels[0] + "." + b.Labels[1]
	case b.Type == "module" && len(b.Labels) == 1:
		return "module." + b.Labels[0]
	case b.Type == "variable" && len(b.Labels) == 1:
		return "var." + b.Labels[0]
	}
	return strings.Join(append([]string{b.Type}, b.Labels...), ".")
}

// policyLocalSource is the tree directory a module call's literal local
// source names, or false.
func policyLocalSource(dir string, b policyBlock) (string, bool) {
	a, ok := b.block.Body.Attributes["source"]
	if !ok {
		return "", false
	}
	src, ok := literalString(a.Expr)
	if !ok {
		return "", false
	}
	src = strings.ReplaceAll(src, `\`, "/")
	if !strings.HasPrefix(src, "./") && !strings.HasPrefix(src, "../") {
		return "", false
	}
	to := path.Join(dir, src)
	if to == ".." || strings.HasPrefix(to, "../") {
		return "", false
	}
	return to, true
}

// fingerprint hashes the tokens of a range with comments and white space
// left out, so a reformat or a comment edit keeps it and any other edit,
// inside a string too, changes it.
func fingerprint(src []byte, r hcl.Range, block bool) string {
	b := r.SliceBytes(src)
	var toks hclsyntax.Tokens
	if block {
		toks, _ = hclsyntax.LexConfig(b, "", hcl.InitialPos)
	} else {
		toks, _ = hclsyntax.LexExpression(b, "", hcl.InitialPos)
	}
	h := sha256.New()
	for _, tk := range toks {
		switch tk.Type {
		case hclsyntax.TokenComment, hclsyntax.TokenNewline, hclsyntax.TokenEOF:
			continue
		}
		h.Write(tk.Bytes)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// policyCompare pairs the documents of both trees by directory and address.
func policyCompare(head, base *policyTree, decided bool) []PolicyPair {
	keys := map[[2]string]bool{}
	for k := range head.docIndex {
		keys[k] = true
	}
	for k := range base.docIndex {
		keys[k] = true
	}
	sorted := slices.SortedFunc(maps.Keys(keys), func(a, b [2]string) int {
		if c := strings.Compare(a[0], b[0]); c != 0 {
			return c
		}
		return strings.Compare(a[1], b[1])
	})
	out := []PolicyPair{}
	for _, k := range sorted {
		p := PolicyPair{Dir: k[0], Address: k[1]}
		h, b := head.docIndex[k], base.docIndex[k]
		switch {
		case !decided || len(h) > 1 || len(b) > 1:
			p.Presence = PresenceUndecidable
		case len(h) == 1 && len(b) == 1:
			p.Presence = PresenceBoth
			hd, bd := &head.report.Documents[h[0]], &base.report.Documents[b[0]]
			p.Content = new(compareKeys(hd.contentKey, bd.contentKey))
			p.Uses = new(compareKeys(hd.usesKey, bd.usesKey))
		case len(h) == 1:
			p.Presence = PresenceHeadOnly // both trees pass the precondition: no directory is undecidable
		default:
			p.Presence = PresenceBaseOnly
		}
		out = append(out, p)
	}
	return out
}

func compareKeys(h, b string) string {
	switch {
	case h == "" || b == "":
		return CompareUndecidable
	case h == b:
		return CompareEqual
	}
	return CompareDiffers
}

// canonJSON is the JSON text of v, for keys and sorting.
func canonJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "\x00" + err.Error()
	}
	return strings.TrimSuffix(buf.String(), "\n")
}
