// Package fixture checks the scratch tree a verification ladder level runs
// against a default-deny allowlist and hashes it.
//
// The tree is the final directory the host hands to Terraform: only .tf files
// directly in it, each parsed and every block, argument, function call and
// reference checked against the grammar of its level. Nothing is evaluated:
// the only values read are literals, by their syntax.
package fixture

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/facts"
)

// SchemaVersion is the schema_version of a Result.
const SchemaVersion = 1

// Limits of one tree. A tree over a limit is a violation, not an error.
const (
	MaxFiles      = 64        // directory entries, of every kind
	MaxFileBytes  = 256 << 10 // one file
	MaxTotalBytes = 1 << 20   // all files
	MaxViolations = 200       // violations listed; one RuleLimitViolations entry marks the rest
)

// Rule ids of a violation.
const (
	RuleFileNotTF              = "file.not-tf"
	RuleFileOverride           = "file.override"
	RuleFileSubdirectory       = "file.subdirectory"
	RuleFileSymlink            = "file.symlink"
	RuleFileSpecial            = "file.special"
	RuleLimitFileSize          = "limit.file-size"
	RuleLimitFileCount         = "limit.file-count"
	RuleLimitTotalSize         = "limit.total-size"
	RuleLimitHCL               = "limit.hcl"
	RuleHCLParseError          = "hcl.parse-error"
	RuleBlockDenied            = "block.denied"
	RuleBlockAttributeDenied   = "block.attribute-denied"
	RuleProviderNotPinned      = "terraform.provider-not-pinned"
	RuleProviderDenied         = "provider.denied"
	RuleProviderValueDenied    = "provider.value-denied"
	RuleResourceTypeDenied     = "resource.type-denied"
	RuleResourceUnderTestCount = "resource.under-test-count"
	RuleExprFunctionDenied     = "expr.function-denied"
	RuleExprReferenceDenied    = "expr.reference-denied"
	RuleExprHeredocTemplate    = "expr.heredoc-template"
	// RuleLimitViolations marks a list cut at MaxViolations.
	RuleLimitViolations = "limit.violations"
)

// Rules are the rules a tree can break, RuleLimitViolations aside.
var Rules = []string{
	RuleFileNotTF, RuleFileOverride, RuleFileSubdirectory, RuleFileSymlink, RuleFileSpecial,
	RuleLimitFileSize, RuleLimitFileCount, RuleLimitTotalSize, RuleLimitHCL, RuleHCLParseError,
	RuleBlockDenied, RuleBlockAttributeDenied, RuleProviderNotPinned, RuleProviderDenied, RuleProviderValueDenied,
	RuleResourceTypeDenied, RuleResourceUnderTestCount,
	RuleExprFunctionDenied, RuleExprReferenceDenied, RuleExprHeredocTemplate,
}

// Options select the tree and its grammar.
type Options struct {
	Dir      string
	Level    string // a key of levels; "" is L1
	Resource string // the one resource type under test, at a level with one
	Pins     []Pin  // the provider versions required_providers may name
}

// Result is the report of one check.
type Result struct {
	SchemaVersion int    `json:"schema_version"`
	OK            bool   `json:"ok"`
	TreeSHA256    string `json:"tree_sha256"`
	// Violations are sorted by file, line, rule and detail. Line 0 is the
	// whole file, and file "." the whole tree.
	Violations []Violation `json:"violations"`
}

// Violation is one place the tree leaves the grammar.
type Violation struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Rule string `json:"rule"`
	// Detail names what was refused, such as a function or block type, when
	// that name is a plain identifier. Hostile text is never echoed.
	Detail string `json:"detail,omitempty"`
}

var (
	tfName     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*\.tf$`)
	plainName  = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
	resourceRE = regexp.MustCompile(`^[a-z][a-z0-9]*_[a-z0-9_]+$`)
)

// Check reads the tree in o.Dir and checks it. An error is bad options or a
// directory that cannot be read; everything the tree itself holds is a
// violation.
func Check(o Options) (*Result, error) {
	lv, err := o.level()
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(o.Dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", o.Dir)
	}
	r, err := os.OpenRoot(o.Dir)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	c := newChecker(lv, o.Resource, o.Pins)
	files, sum, err := c.readTree(r)
	if err != nil {
		return nil, err
	}
	for _, e := range files {
		c.file(e.Path, e.Data)
	}
	if files != nil {
		c.finish()
	}
	return c.result(sum), nil
}

// level returns the grammar o selects, with o.Resource checked against it.
func (o Options) level() (level, error) {
	name := cmp.Or(o.Level, "L1")
	lv, ok := levels[name]
	if !ok {
		return level{}, fmt.Errorf("unknown level %q, want one of %s", o.Level, strings.Join(Levels(), ", "))
	}
	switch {
	case !lv.underTest && o.Resource != "":
		return level{}, fmt.Errorf("level %s takes no resource under test", name)
	case lv.underTest && o.Resource == "":
		return level{}, fmt.Errorf("level %s needs the resource type under test", name)
	case !lv.underTest:
		return lv, nil
	}
	if !resourceRE.MatchString(o.Resource) || o.Resource == "terraform_data" {
		return level{}, fmt.Errorf("resource type %q is not a provider resource type", o.Resource)
	}
	if p := providerOf(o.Resource); p == "terraform" || !lv.allows(p) {
		return level{}, fmt.Errorf("resource type %q: provider %q has no allowlist at level %s", o.Resource, p, name)
	}
	return lv, nil
}

func providerOf(resourceType string) string {
	p, _, _ := strings.Cut(resourceType, "_")
	return p
}

// checker holds the state of one Check.
type checker struct {
	lv         level
	underTest  string
	pins       map[[2]string]bool
	violations []Violation

	declared       map[string]bool // local names required_providers declares
	providerBlocks map[string]int  // provider blocks by local name
	tested         []place         // resources of the type under test
}

type place struct {
	file string
	line int
}

func newChecker(lv level, underTest string, pins []Pin) *checker {
	c := &checker{lv: lv, underTest: underTest, pins: map[[2]string]bool{}, declared: map[string]bool{}, providerBlocks: map[string]int{}}
	for _, p := range pins {
		c.pins[[2]string{p.Source, p.Version}] = true
	}
	return c
}

func (c *checker) add(file string, line int, rule, detail string) {
	if !plainName.MatchString(detail) {
		detail = ""
	}
	c.violations = append(c.violations, Violation{File: file, Line: line, Rule: rule, Detail: detail})
}

// readTree lists the directory, hashes every entry and reads the regular
// files. It returns the .tf files to parse, or nil when a tree limit stops
// the read, and the tree hash: SHA-256 over one "name NUL mode NUL
// content-sha256 LF" line per entry, sorted by name, the content hash empty
// for an entry not read. The hash covers a tree that passes completely.
func (c *checker) readTree(r *os.Root) ([]*facts.Entry, string, error) {
	h := sha256.New()
	ents, err := listRoot(r)
	if err != nil {
		return nil, "", err
	}
	if len(ents) > MaxFiles {
		c.add(".", 0, RuleLimitFileCount, "")
		return nil, hex.EncodeToString(h.Sum(nil)), nil
	}
	files := []*facts.Entry{}
	total := 0
	for _, name := range ents {
		info, err := r.Lstat(name)
		var mode fs.FileMode
		sum := ""
		switch {
		case err != nil:
			c.add(name, 0, RuleFileSpecial, "")
		case info.IsDir():
			mode = info.Mode()
			c.add(name, 0, RuleFileSubdirectory, "")
		case info.Mode()&fs.ModeSymlink != 0:
			mode = info.Mode()
			c.add(name, 0, RuleFileSymlink, "")
		case !info.Mode().IsRegular():
			mode = info.Mode()
			c.add(name, 0, RuleFileSpecial, "")
		default:
			mode = info.Mode()
			e := facts.ReadRegular(r, name, MaxFileBytes)
			switch e.Kind {
			case facts.KindFile:
			case facts.KindOversize:
				c.add(name, 0, RuleLimitFileSize, "")
			default: // swapped or unreadable after the listing
				c.add(name, 0, RuleFileSpecial, "")
			}
			if e.Kind != facts.KindFile {
				break
			}
			if total += len(e.Data); total > MaxTotalBytes {
				c.add(".", 0, RuleLimitTotalSize, "")
				return nil, hex.EncodeToString(h.Sum(nil)), nil
			}
			sum = e.SHA256
			switch {
			case !tfName.MatchString(name):
				c.add(name, 0, RuleFileNotTF, "")
			case name == "override.tf" || strings.HasSuffix(name, "_override.tf"):
				c.add(name, 0, RuleFileOverride, "")
			default:
				files = append(files, e)
			}
		}
		fmt.Fprintf(h, "%s\x00%s\x00%s\n", name, mode, sum)
	}
	return files, hex.EncodeToString(h.Sum(nil)), nil
}

// listRoot returns the names in the root directory, sorted, reading at most
// MaxFiles+1 of them.
func listRoot(r *os.Root) ([]string, error) {
	f, err := r.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var names []string
	for len(names) <= MaxFiles {
		batch, err := f.ReadDir(MaxFiles + 1)
		for _, d := range batch {
			names = append(names, d.Name())
		}
		if errors.Is(err, io.EOF) || (err == nil && len(batch) == 0) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(names)
	return names, nil
}

// file parses one .tf file and checks its tokens and body.
func (c *checker) file(name string, src []byte) {
	body, tokens, err := facts.ParseBody(name, src, facts.DefaultLimits)
	if err != nil {
		rule := RuleHCLParseError
		if facts.IsLimitError(err) {
			rule = RuleLimitHCL
		}
		c.add(name, 0, rule, "")
		return
	}
	c.heredocs(name, tokens)
	c.top(name, body)
}

// heredocs refuses a heredoc holding an interpolation or a directive, once
// per heredoc: a heredoc may only be a plain string.
func (c *checker) heredocs(name string, tokens hclsyntax.Tokens) {
	in, reported := false, false
	for _, tk := range tokens {
		switch tk.Type {
		case hclsyntax.TokenOHeredoc:
			in, reported = true, false
		case hclsyntax.TokenCHeredoc:
			in = false
		case hclsyntax.TokenTemplateInterp, hclsyntax.TokenTemplateControl:
			if in && !reported {
				c.add(name, tk.Range.Start.Line, RuleExprHeredocTemplate, "")
				reported = true
			}
		}
	}
}

// finish checks what spans files: the resource under test, once, and its
// provider declared in required_providers.
func (c *checker) finish() {
	if !c.lv.underTest {
		return
	}
	switch len(c.tested) {
	case 0:
		c.add(".", 0, RuleResourceUnderTestCount, c.underTest)
	case 1:
	default:
		for _, p := range c.tested[1:] {
			c.add(p.file, p.line, RuleResourceUnderTestCount, c.underTest)
		}
	}
	p := providerOf(c.underTest)
	if !c.declared[p] {
		for _, t := range c.tested {
			c.add(t.file, t.line, RuleProviderNotPinned, p)
		}
	}
	if c.providerBlocks[p] == 0 {
		c.add(".", 0, RuleProviderValueDenied, p)
	}
}

func (c *checker) result(sum string) *Result {
	v := c.violations
	slices.SortFunc(v, func(a, b Violation) int {
		return cmp.Or(strings.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line),
			strings.Compare(a.Rule, b.Rule), strings.Compare(a.Detail, b.Detail))
	})
	v = slices.Compact(v)
	if len(v) > MaxViolations {
		v = append(v[:MaxViolations:MaxViolations], Violation{File: ".", Line: 0, Rule: RuleLimitViolations})
	}
	if v == nil {
		v = []Violation{}
	}
	return &Result{SchemaVersion: SchemaVersion, OK: len(v) == 0, TreeSHA256: sum, Violations: v}
}
