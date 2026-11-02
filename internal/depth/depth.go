package depth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/modulestf/facts/internal/behaviour"
	"github.com/modulestf/facts/internal/facts"
)

// Options are the inputs of Run.
type Options struct {
	Head  string // the head tree
	Base  string // the merge base tree
	Files string // the pull request files list, GitHub's pulls/<n>/files shape
	// Classify and Affected are the behaviour --json and affected --json
	// reports of the same change. Optional: a missing one makes the change
	// deep.
	Classify string
	Affected string
	// Examples are example directories the host knows of, besides those
	// the affected report lists. Each is selected unless proven out of
	// scope.
	Examples []string
	Limits   facts.Limits
}

// Report is the output of mtf-facts depth.
type Report struct {
	Format             int      `json:"format"`
	Depth              string   `json:"depth"`
	Reasons            []string `json:"reasons"`
	ChecksOutOfScope   []string `json:"checks_out_of_scope"`
	ExamplesSelected   []string `json:"examples_selected"`
	ExamplesOutOfScope []string `json:"examples_out_of_scope"`
	TouchedTypes       []string `json:"touched_types"`
	Walk               Walk     `json:"walk"`
	Limits             Counts   `json:"limits"`
	SchemaProviders    []string `json:"schema_providers"`
	SchemaTypes        []string `json:"schema_types"`
	SchemaLocks        []string `json:"schema_locks"`
}

// Walk is the parser's coverage of both trees.
type Walk struct {
	Complete bool `json:"complete"`
	Unwalked int  `json:"unwalked"`
}

// Counts are the D-4 limit counts of the change.
type Counts struct {
	TFFiles          int `json:"tf_files"`
	ChangedLines     int `json:"changed_lines"`
	ChangedBlocks    int `json:"changed_blocks"`
	SelectedExamples int `json:"selected_examples"`
}

// UsageError is an input that cannot be used at all: a missing tree or a
// files list that is not one.
type UsageError struct{ msg string }

func (e UsageError) Error() string { return e.msg }

// maxLineCount bounds a files list entry's additions and deletions, so
// their sum cannot overflow.
const maxLineCount = 10_000_000

// maxInputBytes bounds each JSON input file.
const maxInputBytes = 64 << 20

// fileEntry is one entry of the files list.
type fileEntry struct {
	Filename         string  `json:"filename"`
	Status           string  `json:"status"`
	PreviousFilename *string `json:"previous_filename"`
	Additions        *int    `json:"additions"`
	Deletions        *int    `json:"deletions"`
}

// Run decides the depth of the change from the base to the head tree.
func Run(o Options) (*Report, error) {
	for _, d := range []string{o.Head, o.Base} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			return nil, UsageError{fmt.Sprintf("%q is not a directory", d)}
		}
	}
	var list []fileEntry
	if err := readJSON(o.Files, &list, false); err != nil {
		return nil, UsageError{fmt.Sprintf("files list: %v", err)}
	}
	for _, f := range list {
		if f.Additions != nil && *f.Additions > maxLineCount || f.Deletions != nil && *f.Deletions > maxLineCount {
			return nil, UsageError{fmt.Sprintf("files list: %q has a line count over %d", f.Filename, maxLineCount)}
		}
	}
	for _, ex := range o.Examples {
		if !safePath(ex) {
			return nil, UsageError{fmt.Sprintf("example %q is not a safe relative path", ex)}
		}
	}
	r := &run{o: o, reasons: map[string]bool{}}
	head, err := facts.ReadTree(o.Head, o.Limits)
	if err == nil {
		var base *facts.Tree
		if base, err = facts.ReadTree(o.Base, o.Limits); err == nil {
			return r.evaluate(head, base, list)
		}
	}
	// A tree over a read limit or the deadline cannot be walked: deep,
	// with nothing proven.
	return Unwalked(), nil
}

// Unwalked is the report of a run that could not walk the trees, a limit
// or the deadline hit: deep, with deep-unwalked.
func Unwalked() *Report {
	r := &run{reasons: map[string]bool{ReasonUnwalked: true}}
	return r.report(Deep, nil, nil, nil, Walk{Unwalked: 1}, Counts{})
}

// run holds the state of one evaluation.
type run struct {
	o       Options
	reasons map[string]bool
}

func (r *run) add(reason string) { r.reasons[reason] = true }

// readJSON decodes one JSON input file strictly when strict is set.
func readJSON(name string, v any, strict bool) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxInputBytes {
		return fmt.Errorf("over %d bytes", maxInputBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data")
	}
	return nil
}

func (r *run) evaluate(head, base *facts.Tree, list []fileEntry) (*Report, error) {
	changed := r.changes(head, base, list)
	counts := Counts{}
	for _, f := range list {
		if f.Additions == nil || f.Deletions == nil || *f.Additions < 0 || *f.Deletions < 0 {
			r.add(ReasonLimits) // a count the list does not give is over the limit
			continue
		}
		counts.ChangedLines += *f.Additions + *f.Deletions
	}
	r.paths(head, base, changed, list)

	hm, bm := parseTree(head, r.o.Limits), parseTree(base, r.o.Limits)
	bad := map[string]bool{}
	for p := range hm.bad {
		bad[p] = true
	}
	for p := range bm.bad {
		bad[p] = true
	}
	if len(bad) > 0 {
		r.add(ReasonParseFailure)
	}
	root, err := os.OpenRoot(r.o.Head)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	links := &linker{tree: head, root: root}

	sc := newScanner(hm, links, r.o.Limits, changed)
	sc.run()
	if timeUp(r.o.Limits) {
		return Unwalked(), nil
	}
	an := newAnalysis(hm, bm, links)
	tfChange := r.structural(an, hm, bm, changed, &counts)
	for _, l := range sc.hits {
		tfChange = true
		if l.it != nil && l.a != nil {
			an.flow(l.it, l.a, true)
		}
	}
	an.close()
	unwalked := len(bad)
	if sc.over || len(an.seen) > maxNodes {
		unwalked++
	}
	walk := Walk{Complete: unwalked == 0, Unwalked: unwalked}
	if !walk.Complete {
		r.add(ReasonUnwalked)
	}
	counts.ChangedBlocks = an.blocks

	cls, clsOK := r.classifier(changed)
	aff := r.affected()
	exAll, exSel := r.examples(hm, an, aff, changed, sc)
	counts.SelectedExamples = len(exSel)

	var touched []string
	for t := range an.touched {
		if schemaTypeRe.MatchString(t) {
			touched = append(touched, t)
		} else {
			an.add(ReasonUnwalked)
		}
	}
	touched = sortedUnique(touched)

	schema := [3][]string{
		sortedUnique(append(schemaProviders("head", hm), schemaProviders("base", bm)...)),
		schemaTypes(hm),
		schemaLocks(hm),
	}

	// Light: rules 1 to 5, with nothing deep.
	light := len(r.reasons) == 0 && clsOK && cls.Inert && len(sc.hits) == 0 && len(sc.unresolved) == 0 && !sc.strict
	for _, p := range changed {
		if !docPath(p) || tfForm(p) != formNone {
			light = false
		}
	}
	if light {
		if len(changed) > maxLightFiles {
			r.add(ReasonTooManyFiles)
			return r.report(Deep, exAll, nil, touched, walk, counts).withSchema(schema), nil
		}
		r.add(ReasonLightDocsOnly)
		rep := r.report(Light, exAll, nil, touched, walk, counts).withSchema(schema)
		rep.ChecksOutOfScope = []string{CheckA, CheckB, CheckD, CheckGLadder}
		return rep, nil
	}

	// Focused: a Terraform change within D-4 and no deep class.
	maps.Copy(r.reasons, an.reasons)
	if !tfChange {
		r.add(ReasonNonTerraformChange)
	}
	if len(sc.unresolved) > 0 {
		r.add(ReasonUnresolvedFileDependency)
	}
	if counts.TFFiles > maxTFFiles || counts.ChangedLines > maxChangedLines || counts.ChangedBlocks > maxChangedBlocks {
		r.add(ReasonLimits)
	}
	if counts.SelectedExamples > maxSelectedExample {
		r.add(ReasonTooManyExamples)
	}
	if clsOK && !allowed(cls, hm, bm) {
		r.add(ReasonClassifierCategory)
	}
	if len(r.reasons) > 0 {
		return r.report(Deep, exAll, nil, touched, walk, counts).withSchema(schema), nil
	}
	r.add(ReasonFocusedWithinLimits)
	var out []string
	for _, ex := range exAll {
		if !slices.Contains(exSel, ex) {
			out = append(out, ex)
		}
	}
	rep := r.report(Focused, exSel, out, touched, walk, counts).withSchema(schema)
	if len(touched) == 0 {
		rep.ChecksOutOfScope = append(rep.ChecksOutOfScope, CheckB)
	}
	// Check D stays in scope at focused, so it can report a missing example.
	exampleChanged := slices.ContainsFunc(changed, func(p string) bool { return strings.HasPrefix(p, "examples/") })
	if len(exSel) == 0 && !exampleChanged {
		rep.Reasons = append(rep.Reasons, ReasonFocusedNoExample)
	}
	return rep, nil
}

// report builds the report of a depth with every list non-nil.
func (r *run) report(depth string, sel, out, touched []string, walk Walk, counts Counts) *Report {
	reasons := slices.Collect(maps.Keys(r.reasons))
	slices.SortFunc(reasons, func(a, b string) int { return reasonRank(a) - reasonRank(b) })
	return &Report{Format: Format, Depth: depth, Reasons: reasons, ChecksOutOfScope: []string{},
		ExamplesSelected: sortedUnique(slices.Clone(sel)), ExamplesOutOfScope: sortedUnique(slices.Clone(out)),
		TouchedTypes: sortedUnique(slices.Clone(touched)), Walk: walk, Limits: counts,
		SchemaProviders: []string{}, SchemaTypes: []string{}, SchemaLocks: []string{}}
}

func (rep *Report) withSchema(s [3][]string) *Report {
	rep.SchemaProviders, rep.SchemaTypes, rep.SchemaLocks = s[0], s[1], s[2]
	return rep
}

// changes returns the changed paths: every path whose entry differs between
// the trees, and every path the files list names. A list that disagrees
// with the trees makes the change deep.
func (r *run) changes(head, base *facts.Tree, list []fileEntry) []string {
	tree := map[string]bool{}
	for _, t := range []*facts.Tree{head, base} {
		for _, p := range t.Paths {
			if h, b := head.Entries[p], base.Entries[p]; h == nil || b == nil || !h.Same(b) {
				tree[p] = true
			}
		}
	}
	named := map[string]bool{}
	for _, f := range list {
		named[f.Filename] = true
		if f.PreviousFilename != nil {
			named[*f.PreviousFilename] = true
		}
	}
	if !maps.Equal(tree, named) {
		r.add(ReasonFilesListMismatch)
	}
	maps.Copy(tree, named)
	return slices.Sorted(maps.Keys(tree))
}

// paths applies the path rules: unsafe names, case-folded collisions,
// changed links and special files, CI and CLI configuration, wrappers.
func (r *run) paths(head, base *facts.Tree, changed []string, list []fileEntry) {
	for _, p := range changed {
		if !safePath(p) {
			r.add(ReasonPathUnsafe)
		}
		for _, e := range []*facts.Entry{head.Entries[p], base.Entries[p]} {
			if e != nil && (e.Kind == facts.KindSymlink || e.Kind == facts.KindSpecial) {
				r.add(ReasonPathUnsafe)
			}
		}
		if ciPath(p) {
			r.add(ReasonCIConfig)
		}
		if wrappersPath(p) {
			r.add(ReasonWrappers)
		}
		switch tfForm(p) {
		case formCLI:
			r.add(ReasonCLIConfig)
		case formLock:
			r.add(ReasonLockFile)
		case formTest:
			r.add(ReasonTestOrMock)
		}
	}
	for _, f := range list {
		if f.Status == "" {
			r.add(ReasonFilesListMismatch)
		}
	}
	// Case-folded collisions among every path and directory of both trees.
	folded := map[string]string{}
	note := func(p string) {
		for _, q := range append([]string{p}, ancestors(p)...) {
			k := strings.ToLower(q)
			if prev, ok := folded[k]; ok && prev != q {
				r.add(ReasonPathUnsafe)
			}
			folded[k] = q
		}
	}
	for _, t := range []*facts.Tree{head, base} {
		for _, p := range t.Paths {
			note(p)
		}
	}
	for _, p := range changed {
		note(p)
	}
}

// structural applies the deep classes of the changed files and runs the
// structural diff of each changed configuration directory and variables
// file. It returns whether a Terraform-read file changed.
func (r *run) structural(an *analysis, hm, bm *model, changed []string, counts *Counts) bool {
	tf := false
	dirs := map[string]bool{}
	for _, p := range changed {
		form := tfForm(p)
		if form == formNone {
			continue
		}
		tf = true
		counts.TFFiles++
		switch form {
		case formConfig:
			dirs[path.Dir(p)] = true
		case formVars:
			an.diffFile(bm.files[p], hm.files[p])
		case formHCLFile:
			r.add(ReasonUnwalked) // an HCL file Terraform reads that is not modeled
		}
	}
	for _, d := range slices.Sorted(maps.Keys(dirs)) {
		an.diffDir(d)
	}
	return tf
}

// classifier loads the classifier report. ok is false when it is missing,
// invalid, or not of this change.
func (r *run) classifier(changed []string) (*behaviour.Report, bool) {
	var rep behaviour.Report
	if r.o.Classify == "" || readJSON(r.o.Classify, &rep, true) != nil || rep.SchemaVersion != behaviour.SchemaVersion || rep.Capped < 0 {
		r.add(ReasonClassifierUnavailable)
		return &rep, false
	}
	listed := map[string]bool{}
	for _, e := range rep.Entries {
		listed[e.Path] = true
		if e.PreviousPath != nil {
			listed[*e.PreviousPath] = true
		}
	}
	for _, p := range changed {
		if !listed[p] {
			r.add(ReasonClassifierUnavailable)
			return &rep, false
		}
	}
	return &rep, true
}

// affected loads the affected report; nil when it is missing, invalid or
// fell back.
func (r *run) affected() *facts.AffectedReport {
	var rep facts.AffectedReport
	if r.o.Affected == "" || readJSON(r.o.Affected, &rep, true) != nil ||
		rep.SchemaVersion != facts.AffectedSchemaVersion || rep.Fallback != nil {
		r.add(ReasonAffectedFallback)
		return nil
	}
	for _, e := range rep.Examples {
		switch e.Reason {
		case facts.ReasonTouched, facts.ReasonCaller, facts.ReasonAll:
		default:
			r.add(ReasonAffectedFallback)
			return nil
		}
		if !safePath(e.Dir) {
			r.add(ReasonPathUnsafe)
			return nil
		}
	}
	return &rep
}

// examples returns every known example and the ones a focused review
// selects: touched or caller in the affected report, not in it, holding a
// changed path, or reaching a changed directory or the closure through
// local module calls.
func (r *run) examples(hm *model, an *analysis, aff *facts.AffectedReport, changed []string, sc *scanner) (all, sel []string) {
	reason := map[string]string{}
	if aff != nil {
		for _, e := range aff.Examples {
			reason[e.Dir] = e.Reason
		}
	}
	known := map[string]bool{}
	for d := range reason {
		known[d] = true
	}
	for _, d := range hm.configDirs() {
		if path.Dir(d) == "examples" {
			known[d] = true
		}
	}
	for _, d := range r.o.Examples {
		known[d] = true
	}
	reached := map[string]bool{}
	for d := range an.dirs {
		reached[d] = true
	}
	for _, p := range changed {
		reached[path.Dir(p)] = true
	}
	for _, l := range sc.hits {
		reached[path.Dir(l.file)] = true
	}
	for _, ex := range slices.Sorted(maps.Keys(known)) {
		if !safePath(ex) {
			r.add(ReasonPathUnsafe)
			continue
		}
		all = append(all, ex)
		rs, listed := reason[ex]
		switch {
		case !listed, rs == facts.ReasonTouched, rs == facts.ReasonCaller,
			slices.ContainsFunc(changed, func(p string) bool { return strings.HasPrefix(p, ex+"/") }),
			reaches(ex, an, reached):
			sel = append(sel, ex)
		}
	}
	return all, sel
}

// reaches reports whether dir or a module it calls through local sources,
// transitively, is in targets.
func reaches(dir string, an *analysis, targets map[string]bool) bool {
	seen := map[string]bool{}
	stack := []string{dir}
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[d] {
			continue
		}
		seen[d] = true
		if targets[d] {
			return true
		}
		mod := an.head.mods[d]
		if mod == nil {
			continue
		}
		for _, it := range mod.items {
			if it.kind != kModule {
				continue
			}
			callee, local, ok := an.callee(it)
			if !ok {
				return true // a call that does not resolve may reach anything
			}
			if local {
				stack = append(stack, callee)
			}
		}
	}
	return false
}

// timeUp reports a run past its deadline.
func timeUp(l facts.Limits) bool { return !l.Deadline.IsZero() && time.Now().After(l.Deadline) }
