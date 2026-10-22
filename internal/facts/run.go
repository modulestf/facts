// Package facts reads Terraform module trees without following links or
// running anything, evaluates the implemented rules per area and builds the
// report the mtf-facts CLI writes.
package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
)

// SchemaVersion is the version of the report format in schema/.
const SchemaVersion = 2

const maxRegistryBytes = 1 << 20

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// Options are the inputs of one run.
type Options struct {
	Head         string // directory of the head tree
	Base         string // optional directory of the tree the change started from
	HeadSHA      string // optional commit the head tree was extracted from
	MergeBaseSHA string // optional commit the base tree was extracted from
	RegistryFile string // optional rule-facts.md to hash
	// SchemaHandoff and SchemaFiles are the optional schema inputs; see
	// applySchema. ProfileDir is the optional module profile directory; see
	// applyProfile.
	SchemaHandoff string   // schema handoff file from the schema job, gzipped or not
	SchemaFiles   []string // terraform providers schema -json files
	ProfileDir    string   // module profile directory in a skills checkout
	Version       string
	BinarySHA256  string
	Limits        Limits
}

// Report is the whole output of one run. Index is left out when no area in
// scope has a block; InterfaceDiff until its builder fills it.
type Report struct {
	Envelope   Envelope        `json:"envelope"`
	Manifest   []ManifestEntry `json:"manifest"`
	Findings   []Finding       `json:"findings"`
	Candidates []Candidate     `json:"candidates"`
	Index      []IndexEntry    `json:"index,omitempty"`
	// InterfaceDiff holds a document of schema/interface-diff.schema.json.
	InterfaceDiff any `json:"interface_diff,omitempty"`
}

// IndexEntry maps the lines of one block or argument to its area-scoped
// subject, "<area>:<address>". Entries nest; the innermost range that holds
// a line names that line's subject, and of entries with the same lines the
// later one, the inner.
type IndexEntry struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Subject   string `json:"subject"`
}

// Envelope binds a report to its inputs and to the binary that wrote it.
type Envelope struct {
	SchemaVersion  int     `json:"schema_version"`
	Tool           Tool    `json:"tool"`
	HeadSHA        *string `json:"head_sha"`
	MergeBaseSHA   *string `json:"merge_base_sha"`
	RegistrySHA256 *string `json:"registry_sha256"`
	Inputs         Inputs  `json:"inputs"`
	// SchemaInputs and ProfileSHA256 are null when the run read no schema
	// handoff or no profile.
	SchemaInputs  *SchemaInputs `json:"schema_inputs"`
	ProfileSHA256 *string       `json:"profile_sha256"`
}

// SchemaInputs binds a report to the schema handoff it read: the handoff's
// own hash, the commits and .tf tree hashes the handoff records, and a hash
// per raw provider schema file.
type SchemaInputs struct {
	HandoffSHA256 string     `json:"handoff_sha256"`
	HeadSHA       string     `json:"head_sha"`
	MergeBaseSHA  string     `json:"merge_base_sha"`
	TreeHashes    TreeHashes `json:"tree_hashes"`
	SchemaFiles   []FileHash `json:"schema_files"`
}

// TreeHashes are the handoff's tree_hash per revision.
type TreeHashes struct {
	Head      string `json:"head"`
	MergeBase string `json:"merge_base"`
}

type FileHash struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Tool struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	BinarySHA256 string `json:"binary_sha256"`
}

type Inputs struct {
	Head TreeInput  `json:"head"`
	Base *TreeInput `json:"base"`
}

type TreeInput struct {
	TreeSHA256 string      `json:"tree_sha256"`
	Files      []FileInput `json:"files"`
}

type FileInput struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
}

// Run reads the trees, evaluates every rule and builds the report.
func Run(o Options) (*Report, error) { return run(o, Rules) }

// run is Run with the rules to evaluate given, so a test can evaluate a
// subset of Rules or a rule that is not in it.
func run(o Options, rules []Rule) (*Report, error) {
	env := Envelope{SchemaVersion: SchemaVersion, Tool: Tool{Name: "mtf-facts", Version: o.Version, BinarySHA256: o.BinarySHA256}}
	var err error
	if env.HeadSHA, err = optionalSHA("head SHA", o.HeadSHA); err != nil {
		return nil, err
	}
	if env.MergeBaseSHA, err = optionalSHA("merge-base SHA", o.MergeBaseSHA); err != nil {
		return nil, err
	}
	if o.RegistryFile != "" {
		sum, err := hashRegistry(o.RegistryFile)
		if err != nil {
			return nil, err
		}
		env.RegistrySHA256 = &sum
	}
	head, err := ReadTree(o.Head, o.Limits)
	if err != nil {
		return nil, fmt.Errorf("head tree: %w", err)
	}
	env.Inputs.Head = treeInput(head)
	var base *Tree
	if o.Base != "" {
		if base, err = ReadTree(o.Base, o.Limits); err != nil {
			return nil, fmt.Errorf("base tree: %w", err)
		}
		bi := treeInput(base)
		env.Inputs.Base = &bi
	}
	areas, err := FindAreas(head, base, o.Limits, needsUntouched(rules))
	if err != nil {
		return nil, err
	}
	if env.SchemaInputs, err = applySchema(o, head, base, areas); err != nil {
		return nil, err
	}
	if env.ProfileSHA256, err = applyProfile(o, areas); err != nil {
		return nil, err
	}
	manifest, findings, candidates := Evaluate(areas, rules, BuildChangeMap(head, base, areas))
	if err := CheckManifest(manifest, rules, areas); err != nil {
		return nil, err
	}
	return &Report{Envelope: env, Manifest: manifest, Findings: findings, Candidates: candidates, Index: BuildIndex(areas)}, nil
}

// needsUntouched reports whether any rule is cross-area. Untouched areas
// matter only to a cross-area pass, so they are parsed only then.
func needsUntouched(rules []Rule) bool {
	return slices.ContainsFunc(rules, func(r Rule) bool { return r.CrossArea })
}

func optionalSHA(name, s string) (*string, error) {
	if s == "" {
		return nil, nil
	}
	if !shaPattern.MatchString(s) {
		return nil, fmt.Errorf("%s must be 40 or 64 lowercase hex characters", name)
	}
	return &s, nil
}

func hashRegistry(p string) (string, error) {
	if st, err := os.Lstat(p); err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("registry: %s is not a regular file", p)
	}
	f, err := os.Open(p)
	if err != nil {
		return "", fmt.Errorf("registry: %w", err)
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return "", fmt.Errorf("registry: %s is not a regular file", p)
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxRegistryBytes+1))
	if err != nil {
		return "", fmt.Errorf("registry: %w", err)
	}
	if n > maxRegistryBytes {
		return "", fmt.Errorf("registry: over %d bytes", maxRegistryBytes)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func treeInput(t *Tree) TreeInput {
	ti := TreeInput{TreeSHA256: t.TreeSHA256(), Files: []FileInput{}}
	for _, p := range t.Paths {
		e := t.Entries[p]
		ti.Files = append(ti.Files, FileInput{Path: e.Path, Kind: e.Kind, SHA256: e.SHA256})
	}
	return ti
}
