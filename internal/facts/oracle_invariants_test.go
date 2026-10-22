package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/registry"
)

// The checks in this file hold the curated oracle fixtures to the schemas,
// to the rule registry and to their own trees. They read the committed
// fixtures as they are, before setupOracle creates links or an empty base,
// and they cover every tool and candidate rule, implemented or not.

// oracleRegistryEnv names a local copy of the reviewer skill's rule-facts.md,
// the one CI fetches at a pinned commit.
const oracleRegistryEnv = "MTF_FACTS_REGISTRY"

// pinnedOracleRegistry parses the registry. It skips without one, except in
// CI, where a skip would hide the checks.
func pinnedOracleRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	p := os.Getenv(oracleRegistryEnv)
	if p == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is not set", oracleRegistryEnv)
		}
		t.Skipf("%s is not set", oracleRegistryEnv)
	}
	data, err := os.ReadFile(p) //nolint:gosec // G304: the path is the CI-set registry copy
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Parse(string(data))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// oracleSchemas loads the facts schemas plus the oracle's expected.schema.json.
func oracleSchemas(t *testing.T) schemaSet {
	t.Helper()
	set := loadSchemas(t)
	data, err := os.ReadFile(filepath.Join(oracleRoot, "expected.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := decodeJSON(t, data).(map[string]any)
	if !ok {
		t.Fatal("expected.schema.json is not an object")
	}
	set[s["$id"].(string)] = s
	return set
}

// fixtureDirs lists the fixture directories under root, sorted.
func fixtureDirs(root string) ([]string, error) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// checkOracleSchemas validates every expected.json against expected.schema.json
// and every schema-handoff.json against schema-handoff.schema.json. Patterns RE2
// cannot compile, the lookaheads of the path patterns, are not checked; see
// schemaSet.check.
func checkOracleSchemas(t *testing.T, set schemaSet, root string) []string {
	t.Helper()
	names, err := fixtureDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	for _, name := range names {
		for file, schema := range map[string]string{
			"expected.json":       "mtf-facts-expected.schema.json",
			"schema-handoff.json": "https://github.com/modulestf/facts/schema/schema-handoff.schema.json",
		} {
			data, err := os.ReadFile(filepath.Join(root, name, file))
			if errors.Is(err, fs.ErrNotExist) && file == "schema-handoff.json" {
				continue
			}
			if err != nil {
				bad = append(bad, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			if set[schema] == nil {
				t.Fatalf("schema %s not loaded", schema)
			}
			for _, e := range set.validate(schema, decodeJSON(t, data)) {
				bad = append(bad, fmt.Sprintf("%s/%s: %s", name, file, e))
			}
		}
	}
	return bad
}

// oracleRule is what the invariants read of a tool or candidate rule.
type oracleRule struct {
	engine   string
	kinds    []string
	severity []string
	cross    bool
}

// oracleRuleSet returns the tool and candidate rules of the registry, and
// their ids in registry order.
func oracleRuleSet(reg *registry.Registry) (map[string]oracleRule, []string) {
	rules := map[string]oracleRule{}
	var order []string
	for _, r := range reg.Rules {
		if r.Engine == registry.EngineTool || r.Engine == registry.EngineCandidate {
			rules[r.ID] = oracleRule{engine: r.Engine, kinds: r.AreaKinds, severity: r.Severities, cross: r.CrossArea}
			order = append(order, r.ID)
		}
	}
	return rules, order
}

// oracleReasons is the closed list of reason codes.
var oracleReasons = []string{"schema_unavailable", "no_base", "parse_error", "unsupported_file", "symlink", "limit_exceeded", "profile_unavailable"}

var (
	areaDirPattern  = regexp.MustCompile(`^(modules|examples)/[^/]+$`)
	areaFilePattern = regexp.MustCompile(`^(modules|examples)/[^/]+/`)
)

func oracleAreaKind(area string) string {
	switch {
	case area == ".":
		return "root"
	case !areaDirPattern.MatchString(area):
		return ""
	case strings.HasPrefix(area, "modules/"):
		return "submodule"
	}
	return "example"
}

// oracleAreaOf is the area of a file: .github/, tests/, the root profile files
// and a file directly under modules/ or examples/ fall to the root.
func oracleAreaOf(file string) string {
	if m := areaFilePattern.FindString(file); m != "" {
		return strings.TrimSuffix(m, "/")
	}
	return "."
}

// oracleFixture is one committed fixture directory.
type oracleFixture struct {
	dir   string
	links [][2]string // <link> <target> from symlinks.txt
}

func readOracleFixture(dir string) (*oracleFixture, error) {
	f := &oracleFixture{dir: dir}
	data, err := os.ReadFile(filepath.Join(dir, "symlinks.txt"))
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	for l := range strings.Lines(string(data)) {
		fields := strings.Fields(l)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("symlinks.txt: want <link> <target>: %q", l)
		}
		f.links = append(f.links, [2]string{fields[0], fields[1]})
	}
	return f, nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// isAreaDir reports whether p is modules/<name> or examples/<name> and a
// directory, or a link symlinks.txt creates to a directory, in head/ or base/.
func (f *oracleFixture) isAreaDir(p string) bool {
	if !areaDirPattern.MatchString(p) {
		return false
	}
	if isDir(filepath.Join(f.dir, "head", p)) || isDir(filepath.Join(f.dir, "base", p)) {
		return true
	}
	for _, l := range f.links {
		tree, rel, _ := strings.Cut(l[0], "/")
		// resolved lexically from the link's directory, as setupOracle does
		if rel == p && isDir(filepath.Join(f.dir, tree, path.Clean(path.Join(path.Dir(p), l[1])))) {
			return true
		}
	}
	return false
}

func (f *oracleFixture) pathArea(p string) string {
	if f.isAreaDir(p) {
		return p
	}
	return oracleAreaOf(p)
}

// treeEntry is a file's mode and content, or a link's target.
type treeEntry struct {
	mode    fs.FileMode
	content string
	link    string
}

// treeEntries maps the paths of the files in tree, and of the links
// symlinks.txt creates there, to their entries.
func (f *oracleFixture) treeEntries(tree string) (map[string]treeEntry, error) {
	out := map[string]treeEntry{}
	root := filepath.Join(f.dir, tree)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && p == root {
			return filepath.SkipDir
		}
		if err != nil || d.IsDir() {
			return err
		}
		st, err := os.Stat(p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p) //nolint:gosec // G304: a committed fixture file
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = treeEntry{mode: st.Mode(), content: string(data)}
		return err
	})
	for _, l := range f.links {
		if rel, ok := strings.CutPrefix(l[0], tree+"/"); ok {
			out[rel] = treeEntry{link: l[1]}
		}
	}
	return out, err
}

// changedAreas returns the areas the change touches: an entry added, removed,
// or changed in content, mode or link target between base/ and head/. With no
// base, every area of head/.
func (f *oracleFixture) changedAreas() ([]string, error) {
	head, err := f.treeEntries("head")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	_, markerErr := os.Stat(filepath.Join(f.dir, "empty-base"))
	if !isDir(filepath.Join(f.dir, "base")) && markerErr != nil {
		for p := range head {
			set[f.pathArea(p)] = true
		}
	} else {
		base, err := f.treeEntries("base")
		if err != nil {
			return nil, err
		}
		for p, h := range head {
			if b, ok := base[p]; !ok || b != h {
				set[f.pathArea(p)] = true
			}
		}
		for p := range base {
			if _, ok := head[p]; !ok {
				set[f.pathArea(p)] = true
			}
		}
	}
	return sortedKeys(set), nil
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// treeInputs is the handoff's inputs entry for a tree: the .tf files that are
// not links with their SHA-256, sorted by path, and the hash of their
// "<sha256>  <path>" lines.
func treeInputs(tree string) (map[string]any, error) {
	type file struct{ path, sum string }
	var files []file
	err := filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && p == tree {
			return filepath.SkipDir
		}
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".tf") || d.Type()&fs.ModeSymlink != 0 {
			return err
		}
		data, err := os.ReadFile(p) //nolint:gosec // G304: a committed fixture file
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(tree, p)
		sum := sha256.Sum256(data)
		files = append(files, file{filepath.ToSlash(rel), hex.EncodeToString(sum[:])})
		return err
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(files, func(a, b file) int { return strings.Compare(a.path, b.path) })
	var pre strings.Builder
	list := []any{}
	for _, f := range files {
		fmt.Fprintf(&pre, "%s  %s\n", f.sum, f.path)
		list = append(list, map[string]any{"path": f.path, "sha256": f.sum})
	}
	sum := sha256.Sum256([]byte(pre.String()))
	return map[string]any{"tree_hash": hex.EncodeToString(sum[:]), "files": list}, nil
}

// fileLines returns the lines of tree/p, or nil when it is not a regular file.
func (f *oracleFixture) fileLines(tree, p string) []string {
	full := filepath.Join(f.dir, tree, filepath.FromSlash(p))
	st, err := os.Lstat(full)
	if err != nil || !st.Mode().IsRegular() {
		return nil
	}
	data, err := os.ReadFile(full) //nolint:gosec // G304: a committed fixture file
	if err != nil {
		return nil
	}
	return strings.Split(string(data), "\n")
}

// blockHeader returns the header pattern of the top-level block an address
// lives in, or nil when the address names no block (a provider source, cited
// at its first use).
func blockHeader(addr string) *regexp.Regexp {
	if strings.HasPrefix(addr, "provider[") {
		return nil
	}
	parts := strings.Split(addr, ".")
	// A short address gets empty names, so its header matches no block.
	parts = append(parts, "", "")
	q := regexp.QuoteMeta
	var p string
	switch parts[0] {
	case "var":
		p = `^variable "` + q(parts[1]) + `" \{`
	case "output", "module":
		p = `^` + parts[0] + ` "` + q(parts[1]) + `" \{`
	case "local":
		p = `^locals \{`
	case "provider":
		p = `^provider "` + q(parts[1]) + `" \{`
	case "required_version", "required_providers":
		p = `^terraform \{`
	case "data":
		p = `^data "` + q(parts[1]) + `" "` + q(parts[2]) + `" \{`
	default:
		p = `^resource "` + q(parts[0]) + `" "` + q(parts[1]) + `" \{`
	}
	return regexp.MustCompile(p)
}

// insideBlock reports whether line, 1-based, falls in a block whose header
// matches, counting braces from the header line.
func insideBlock(lines []string, line int, header *regexp.Regexp) bool {
	for i, l := range lines {
		start := i + 1
		if !header.MatchString(l) {
			continue
		}
		depth := 0
		for j := start; j <= len(lines); j++ {
			depth += strings.Count(lines[j-1], "{") - strings.Count(lines[j-1], "}")
			if start <= line && line <= j {
				return true
			}
			if depth <= 0 {
				break
			}
		}
	}
	return false
}

// oracleItem is a finding or a candidate of expected.json.
type oracleItem struct {
	ID          string `json:"id"`
	CandidateID string `json:"candidate_id"`
	RuleID      string `json:"rule_id"`
	Severity    string `json:"severity"`
	File        string `json:"file"`
	Line        *int   `json:"line"`
	Subject     string `json:"subject"`
	Evidence    struct {
		SourceLines []struct {
			Revision string `json:"revision"`
			File     string `json:"file"`
			Line     int    `json:"line"`
			Text     string `json:"text"`
		} `json:"source_lines"`
	} `json:"evidence"`
}

type oracleExpected struct {
	Manifest []struct {
		RuleID     string  `json:"rule_id"`
		Area       string  `json:"area"`
		Status     string  `json:"status"`
		ReasonCode *string `json:"reason_code"`
	} `json:"manifest"`
	Findings   []oracleItem `json:"findings"`
	Candidates []oracleItem `json:"candidates"`
}

// checkOracleInvariants holds every fixture under root to the registry and to
// its trees, and returns each violation. See the README next to the fixtures
// for the conventions.
func checkOracleInvariants(reg *registry.Registry, root string) []string {
	rules, order := oracleRuleSet(reg)
	names, err := fixtureDirs(root)
	if err != nil {
		return []string{err.Error()}
	}
	var bad []string
	positive := map[string]bool{}
	for _, name := range names {
		for _, e := range checkOracleFixture(filepath.Join(root, name), rules, order, positive) {
			bad = append(bad, name+": "+e)
		}
	}
	for _, r := range order {
		if !positive[r] {
			bad = append(bad, r+": no fixture has a positive case")
		}
	}
	return bad
}

type oracleKey struct{ rule, area string }

// oracleCheck holds one fixture's state while its checks run.
type oracleCheck struct {
	dir      string
	f        *oracleFixture
	rules    map[string]oracleRule
	order    []string
	exp      oracleExpected
	areas    []string             // the areas in scope
	seen     map[oracleKey]string // status by manifest entry
	problems []string
}

func (c *oracleCheck) fail(f string, a ...any) { c.problems = append(c.problems, fmt.Sprintf(f, a...)) }

// home is the area an item's manifest entry is in: a cross-area rule has one
// entry, under ., for the pass over every area.
func (c *oracleCheck) home(rule, file string) string {
	if c.rules[rule].cross {
		return "."
	}
	return c.f.pathArea(file)
}

func checkOracleFixture(dir string, rules map[string]oracleRule, order []string, positive map[string]bool) []string {
	c := &oracleCheck{dir: dir, rules: rules, order: order, seen: map[oracleKey]string{}}
	if !isDir(filepath.Join(dir, "head")) {
		c.fail("no head/")
	}
	var err error
	if c.f, err = readOracleFixture(dir); err != nil {
		return []string{err.Error()}
	}
	data, err := os.ReadFile(filepath.Join(dir, "expected.json")) //nolint:gosec // G304: a committed fixture file
	if err != nil {
		return []string{err.Error()}
	}
	if err := json.Unmarshal(data, &c.exp); err != nil {
		return []string{"expected.json: " + err.Error()}
	}
	for _, step := range []func() error{c.checkScope, func() error { c.checkManifest(positive); return nil },
		func() error { c.checkItems(); return nil }, func() error { c.checkLines(); return nil }, c.checkHandoff} {
		if err := step(); err != nil {
			return append(c.problems, err.Error())
		}
	}
	return c.problems
}

// checkScope holds the areas in scope to the ones the change touches, or
// every area without a base; . only when the change touches it.
func (c *oracleCheck) checkScope() error {
	scope := map[string]bool{}
	for _, m := range c.exp.Manifest {
		if r, ok := c.rules[m.RuleID]; ok && !r.cross {
			scope[m.Area] = true
		}
	}
	c.areas = sortedKeys(scope)
	touched, err := c.f.changedAreas()
	if err != nil {
		return err
	}
	if !slices.Equal(c.areas, touched) {
		c.fail("areas in scope %v, but the change touches %v", c.areas, touched)
	}
	return nil
}

// checkManifest checks each entry against the registry and the closed reason
// code list, and that every rule has its entries; it records the rules with a
// positive case in positive.
func (c *oracleCheck) checkManifest(positive map[string]bool) {
	for _, m := range c.exp.Manifest {
		if m.Status == "finding" || m.Status == "candidate" {
			positive[m.RuleID] = true
		}
		k := oracleKey{m.RuleID, m.Area}
		r, ok := c.rules[m.RuleID]
		if !ok {
			c.fail("manifest names %s, not a tool or candidate rule", m.RuleID)
			continue
		}
		if _, dup := c.seen[k]; dup {
			c.fail("duplicate entry %v", k)
		}
		c.seen[k] = m.Status
		if oracleAreaKind(m.Area) == "" {
			c.fail("area %s is not ., modules/<name> or examples/<name>", m.Area)
		}
		if r.cross && m.Area != "." {
			c.fail("%v: a cross-area rule has its one entry under .", k)
		}
		na := !r.cross && !slices.Contains(r.kinds, oracleAreaKind(m.Area))
		if na != (m.Status == "not_applicable") {
			c.fail("%v: status %s but the area kind is outside area_kinds: %v", k, m.Status, na)
		}
		if (m.Status == "not_evaluated") != (m.ReasonCode != nil) {
			c.fail("%v: reason_code must be present exactly on not_evaluated", k)
		}
		if m.ReasonCode != nil && !slices.Contains(oracleReasons, *m.ReasonCode) {
			c.fail("%v: reason_code %s not in the closed list", k, *m.ReasonCode)
		}
	}
	for _, r := range c.order {
		in := c.areas
		if c.rules[r].cross {
			in = []string{"."}
		}
		for _, a := range in {
			if _, ok := c.seen[oracleKey{r, a}]; !ok {
				c.fail("missing entry %v", oracleKey{r, a})
			}
		}
	}
}

// checkItems checks sequential ids, the rule's engine and severities, and that
// a finding or candidate status sits exactly where items of that rule are.
func (c *oracleCheck) checkItems() {
	for _, kind := range []struct {
		items                  []oracleItem
		status, engine, prefix string
	}{
		{c.exp.Findings, "finding", registry.EngineTool, "F"},
		{c.exp.Candidates, "candidate", registry.EngineCandidate, "C"},
	} {
		for i, it := range kind.items {
			id := it.ID
			if kind.status == "candidate" {
				id = it.CandidateID
			}
			if want := fmt.Sprintf("%s-%03d", kind.prefix, i+1); id != want {
				c.fail("%s id %s out of sequence, want %s", kind.status, id, want)
			}
			r, ok := c.rules[it.RuleID]
			if !ok || r.engine != kind.engine {
				c.fail("%s: %s is not a %s rule", id, it.RuleID, kind.engine)
				continue
			}
			if kind.status == "finding" && !slices.Contains(r.severity, it.Severity) {
				c.fail("%s: severity %s not in %v", id, it.Severity, r.severity)
			}
			home := c.home(it.RuleID, it.File)
			if s, ok := c.seen[oracleKey{it.RuleID, home}]; !ok || s != kind.status {
				c.fail("%s: entry for %s in %s is not %s", id, it.RuleID, home, kind.status)
			}
		}
		for k, s := range c.seen {
			if s == kind.status && !slices.ContainsFunc(kind.items, func(it oracleItem) bool {
				return it.RuleID == k.rule && c.home(k.rule, it.File) == k.area
			}) {
				c.fail("%v is %s with no %s there", k, s, kind.status)
			}
		}
	}
}

// checkLines checks area-scoped subjects, evidence text against the trees and
// a cited line inside the block its subject names.
func (c *oracleCheck) checkLines() {
	for _, it := range append(slices.Clone(c.exp.Findings), c.exp.Candidates...) {
		id := it.ID
		if id == "" {
			id = it.CandidateID
		}
		area := c.f.pathArea(it.File)
		if it.Subject != it.File && !strings.HasPrefix(it.Subject, area+":") {
			c.fail("%s: subject %s is neither the file path nor scoped to area %s", id, it.Subject, area)
		}
		for _, sl := range it.Evidence.SourceLines {
			tree := "base"
			if sl.Revision == "head" {
				tree = "head"
			}
			lines := c.f.fileLines(tree, sl.File)
			if sl.Line < 1 || sl.Line > len(lines) || lines[sl.Line-1] != sl.Text {
				c.fail("%s: source line %s %s:%d does not match the tree", id, sl.Revision, sl.File, sl.Line)
			}
		}
		if it.Line == nil || it.Subject == it.File {
			continue
		}
		lines := c.f.fileLines("head", it.File)
		_, addr, _ := strings.Cut(it.Subject, ":")
		header := blockHeader(addr)
		if *it.Line < 1 || *it.Line > len(lines) {
			c.fail("%s: line %d is not in head/%s", id, *it.Line, it.File)
		} else if header != nil && !insideBlock(lines, *it.Line, header) {
			c.fail("%s: line %d of %s is outside the block of %s", id, *it.Line, it.File, it.Subject)
		}
	}
}

// checkHandoff checks the handoff's file hashes against the fixture trees.
func (c *oracleCheck) checkHandoff() error {
	data, err := os.ReadFile(filepath.Join(c.dir, "schema-handoff.json")) //nolint:gosec // G304: a committed fixture file
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var h struct {
		Inputs map[string]any `json:"inputs"`
	}
	if err := json.Unmarshal(data, &h); err != nil {
		return fmt.Errorf("schema-handoff.json: %w", err)
	}
	for rev, tree := range map[string]string{"head": "head", "merge_base": "base"} {
		want, err := treeInputs(filepath.Join(c.dir, tree))
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(h.Inputs[rev], want) {
			c.fail("schema-handoff.json inputs.%s does not match %s/", rev, tree)
		}
	}
	return nil
}

func TestOracleSchemas(t *testing.T) {
	if bad := checkOracleSchemas(t, oracleSchemas(t), oracleRoot); len(bad) > 0 {
		t.Errorf("oracle files do not validate:\n%s", strings.Join(bad, "\n"))
	}
}

func TestOracleInvariants(t *testing.T) {
	if bad := checkOracleInvariants(pinnedOracleRegistry(t), oracleRoot); len(bad) > 0 {
		t.Errorf("oracle invariants:\n%s", strings.Join(bad, "\n"))
	}
}
