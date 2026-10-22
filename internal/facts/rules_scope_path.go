package facts

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/modulestf/facts/internal/profile"
)

// scopePathRules is the scope_path rule family: the profile and scope rules
// that read the module profile and the paths the change touches. Each one
// follows the Fails when text of its reviewer check file and the registry
// row of its id. Every rule here is a candidate: the engine finds the paths
// and lines, and a model confirms.
var scopePathRules = []Rule{
	{ID: "profile.region-not-allowed", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedExpression, Kinds: []string{AreaRoot, AreaSubmodule, AreaExample}, Check: checkRegionNotAllowed},
	{ID: "profile.template-drift", Tier: "candidate", Class: ClassPath, Scope: ScopeTouchedBlock, Kinds: []string{AreaRoot}, NeedsBase: true, Check: checkTemplateDrift},
	{ID: "scope.pre-commit-hook", Tier: "candidate", Class: ClassPath, Scope: ScopeTouchedBlock, Kinds: []string{AreaRoot}, NeedsBase: true, Check: checkPreCommitHook},
	{ID: "scope.profile-file", Tier: "candidate", Class: ClassPath, Scope: ScopeTouchedBlock, Kinds: []string{AreaRoot}, NeedsBase: true, Check: checkProfileFile},
	{ID: "scope.readme-banner", Tier: "candidate", Class: ClassPath, Scope: ScopeTouchedBlock, Kinds: []string{AreaRoot}, NeedsBase: true, Check: checkReadmeBanner},
	{ID: "scope.tooling", Tier: "candidate", Class: ClassPath, Scope: ScopeNewlyIntroduced, Kinds: []string{AreaRoot, AreaSubmodule, AreaExample}, NeedsBase: true, Check: checkTooling},
}

// maxEvidenceLines caps the source lines of one path candidate. A cap that
// bites sets evidence_complete to false.
const maxEvidenceLines = 8

// pathEvidence builds the evidence of one candidate and tracks its caps.
type pathEvidence struct {
	ev       Evidence
	complete bool
}

func newPathEvidence() *pathEvidence {
	return &pathEvidence{ev: Evidence{SourceLines: []SourceLine{}, SchemaFacts: []SchemaFact{}}, complete: true}
}

func (p *pathEvidence) text(s string) string {
	if len(s) > maxEvidenceText {
		p.complete = false
		return s[:maxEvidenceText]
	}
	return s
}

func (p *pathEvidence) value(s string) *string {
	t := p.text(s)
	return &t
}

func (p *pathEvidence) line(revision, file string, n int, text []byte) {
	if len(p.ev.SourceLines) == maxEvidenceLines {
		p.complete = false
		return
	}
	p.ev.SourceLines = append(p.ev.SourceLines, SourceLine{Revision: revision, File: file, Line: n, Text: p.text(strings.TrimSuffix(string(text), "\r"))})
}

// candidate is the candidate of a path rule, whose subject is its path.
func (p *pathEvidence) candidate(rule, file string, line *int) Candidate {
	return Candidate{RuleID: rule, File: file, Line: line, Subject: file, Evidence: p.ev, EvidenceComplete: p.complete}
}

// fileAt returns the bytes of the regular file at p in t, and whether t has
// anything at p. A link, special, unreadable or oversize entry has no bytes
// to read: bad is then the not_evaluated result. t may be nil.
func fileAt(t *Tree, p string) (data []byte, present bool, bad *Result) {
	if t == nil {
		return nil, false, nil
	}
	e := t.Entries[p]
	switch {
	case e == nil:
		return nil, false, nil
	case e.Kind != KindFile:
		return nil, true, &Result{NotEvaluated: p + " is " + kindText(e.Kind), Code: kindCode(e.Kind)}
	}
	return e.Data, true, nil
}

// unchanged reports whether the change leaves the entry at p as it was.
func unchanged(a *Area, p string) bool {
	return a.baseTree != nil && sameEntry(a.headTree.Entries[p], a.baseTree.Entries[p])
}

// templateFor returns the template whose destination is p, or nil.
func templateFor(p *profile.Profile, dest string) []byte {
	for _, t := range p.Templates {
		if t.Destination == dest {
			return t.Data
		}
	}
	return nil
}

// headChanged returns the changed head lines of a file against its base, or
// nil when the diff passes its bound. Rules use it only to pick a line; with
// no line a path candidate is judged by its path.
func headChanged(base, head []byte) []lineRange {
	budget := maxDiffTotalCells
	r, ok := changedLines(base, head, &budget)
	if !ok {
		return nil
	}
	return r
}

func inRanges(r []lineRange, line int) bool {
	return slices.ContainsFunc(r, func(x lineRange) bool { return line >= x.Start && line <= x.End })
}

// firstChanged returns the first of lines, in order, that is changed.
func firstChanged(changed []lineRange, lines ...int) *int {
	for _, l := range lines {
		if inRanges(changed, l) {
			return &l
		}
	}
	return nil
}

func lineOf(data []byte, n int) []byte { return splitLines(data)[n-1] }

// Version pins are left out of the template comparison: the version after
// @ in a uses: or a package spec, with a version comment after it, and the
// value of a rev: key or a key naming a version, such as node-version or
// TFLINT_VERSION.
var (
	atPin  = regexp.MustCompile(`(\S)@[0-9A-Za-z][0-9A-Za-z._+-]*(\s+#\s*v?[0-9][0-9A-Za-z._+-]*\s*$)?`)
	keyPin = regexp.MustCompile(`^(\s*(?:-\s+)?(?:rev|[A-Za-z0-9_-]*(?i:version)[A-Za-z0-9_-]*):\s+)["']?v?[0-9][0-9A-Za-z._+-]*["']?(\s*(?:#.*)?)$`)
)

// unpinned masks the version pins of each line. It keeps the line count, so
// a line number of the result is one of the input.
func unpinned(data []byte) []byte {
	var b bytes.Buffer
	for _, l := range splitLines(data) {
		l = atPin.ReplaceAll(l, []byte("${1}@PIN"))
		l = keyPin.ReplaceAll(l, []byte("${1}PIN${2}"))
		b.Write(l)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// templateDiff returns the lines of x that are not in a longest common
// subsequence with the template t, and the number of template lines x
// lacks. ok is false past the diff bounds.
func templateDiff(t, x []byte, budget *int) (extra []lineRange, missing int, ok bool) {
	extra, ok = changedLines(t, x, budget)
	n := 0
	for _, r := range extra {
		n += r.End - r.Start + 1
	}
	return extra, len(splitLines(t)) - (len(splitLines(x)) - n), ok
}

// profile.template-drift: a verbatim profile file the change edits away
// from its template, version pins left out. Drift the base already carries
// is not the change's: a candidate needs a head line that differs from the
// template and that the change wrote, or more template lines missing than
// at the base, as when the change drops template text. A deleted file is
// scope.profile-file. Whether a stated reason allows the drift is prose,
// which the model judges.
func checkTemplateDrift(a *Area) Result {
	const id = "profile.template-drift"
	if res, ok := needProfile(a); !ok {
		return res
	}
	var res Result
	budget := maxDiffTotalCells
	for _, f := range a.Profile.ProfileOwned.Verbatim {
		tpl := templateFor(a.Profile, f)
		head, atHead, bad := fileAt(a.headTree, f)
		base, _, _ := fileAt(a.baseTree, f) // a base that is no file has no lines
		switch {
		case tpl == nil || !atHead || unchanged(a, f):
			continue
		case bad != nil:
			return *bad
		}
		t, h, b := unpinned(tpl), unpinned(head), unpinned(base)
		drift, missHead, ok1 := templateDiff(t, h, &budget)
		_, missBase, ok2 := templateDiff(t, b, &budget)
		changed, ok3 := changedLines(base, head, &budget)
		if !ok1 || !ok2 || !ok3 {
			return Result{NotEvaluated: f + " is past the line diff limit", Code: ReasonLimitExceeded}
		}
		ev := newPathEvidence()
		var line *int
		for _, r := range drift {
			for n := r.Start; n <= r.End; n++ {
				if !inRanges(changed, n) {
					continue
				}
				if line == nil {
					line = &n
					ev.ev.Head = ev.value(strings.TrimSuffix(string(lineOf(head, n)), "\r"))
				}
				ev.line("head", f, n, lineOf(head, n))
			}
		}
		if line == nil && missHead <= missBase {
			continue
		}
		if line == nil {
			// The change only dropped template text: cite the base lines it removed.
			removed, ok := changedLines(head, base, &budget)
			if !ok {
				return Result{NotEvaluated: f + " is past the line diff limit", Code: ReasonLimitExceeded}
			}
			for _, r := range removed {
				for n := r.Start; n <= r.End; n++ {
					if ev.ev.Base == nil {
						ev.ev.Base = ev.value(strings.TrimSuffix(string(lineOf(base, n)), "\r"))
					}
					ev.line("merge_base", f, n, lineOf(base, n))
				}
			}
		}
		res.Candidates = append(res.Candidates, ev.candidate(id, f, line))
	}
	return res
}

// regionPattern matches an AWS region name, such as eu-west-1 or
// us-gov-east-1, and nothing that only contains one.
var regionPattern = regexp.MustCompile(`^[a-z]{2}(-gov|-iso[a-z]?)?-(north|south|east|west|central|northeast|northwest|southeast|southwest)-[0-9]+$`)

// regionLiteral is a quoted string whose whole text is a region name.
type regionLiteral struct {
	file string // relative to the area
	line int
	text string // with its quotes
}

// regionLiterals lexes the .tf files directly in dir and returns the quoted
// literals that are a whole region name and choose one: a literal used as an
// object key does not. A region inside an ARN or a hostname is not a whole
// literal. Interpolated strings and heredocs are not read.
func regionLiterals(t *Tree, dir string) []regionLiteral {
	if t == nil {
		return nil
	}
	var out []regionLiteral
	for _, e := range t.DirEntries(dir) {
		name := path.Base(e.Path)
		if e.Kind != KindFile || !strings.HasSuffix(name, ".tf") || ignoredName(name) {
			continue
		}
		toks, _ := hclsyntax.LexConfig(e.Data, name, hcl.InitialPos)
		for i := 0; i+2 < len(toks); i++ {
			if toks[i].Type != hclsyntax.TokenOQuote || toks[i+1].Type != hclsyntax.TokenQuotedLit ||
				toks[i+2].Type != hclsyntax.TokenCQuote || !regionPattern.Match(toks[i+1].Bytes) || objectKey(toks, i) {
				continue
			}
			out = append(out, regionLiteral{file: name, line: toks[i+1].Range.Start.Line, text: `"` + string(toks[i+1].Bytes) + `"`})
		}
	}
	return out
}

// objectKey reports whether the quoted string opening at toks[i] is an object
// key: followed by = or, after an opening brace, comma or newline, by a colon.
// A colon after any other token ends the true branch of a conditional.
func objectKey(toks hclsyntax.Tokens, i int) bool {
	if i+3 >= len(toks) {
		return false
	}
	switch toks[i+3].Type {
	case hclsyntax.TokenEqual:
		return true
	case hclsyntax.TokenColon:
		if i == 0 {
			return false
		}
		prev := toks[i-1].Type
		return prev == hclsyntax.TokenOBrace || prev == hclsyntax.TokenComma || prev == hclsyntax.TokenNewline
	}
	return false
}

// profile.region-not-allowed: a region literal that is neither region the
// profile allows. The rule's scope keeps only the literals the change sets.
// Its evidence is the literal at head and the first region literal of the
// same subject at base.
func checkRegionNotAllowed(a *Area) Result {
	const id = "profile.region-not-allowed"
	if res, ok := needProfile(a); !ok {
		return res
	}
	allowed := []string{a.Profile.Regions.Primary, a.Profile.Regions.Secondary}
	baseIdx, baseVal := moduleIndex(nil, a.Base), map[string]string{}
	for _, l := range regionLiterals(a.baseTree, a.Path) {
		if s := SubjectAt(baseIdx, l.file, l.line); s != "" && baseVal[s] == "" {
			baseVal[s] = l.text
		}
	}
	headIdx := moduleIndex(nil, a.Head)
	var res Result
	for _, l := range regionLiterals(a.headTree, a.Path) {
		if slices.Contains(allowed, strings.Trim(l.text, `"`)) {
			continue
		}
		file, sub := areaFile(a.Path, l.file), SubjectAt(headIdx, l.file, l.line)
		if sub == "" {
			sub = l.file
		}
		ev := newPathEvidence()
		ev.line("head", file, l.line, lineOf(a.headTree.Entries[file].Data, l.line))
		ev.ev.Head = ev.value(l.text)
		if v, ok := baseVal[sub]; ok {
			ev.ev.Base = ev.value(v)
		}
		line := l.line
		res.Candidates = append(res.Candidates, Candidate{RuleID: id, File: file, Line: &line,
			Subject: subject(a.Path, short(sub)), Evidence: ev.ev, EvidenceComplete: ev.complete})
	}
	return res
}

// scope.readme-banner: the root README.md lacks the profile's banner line,
// byte for byte, and either the base README.md had it or the change creates
// README.md. A README that lacked it at the base and still does is not the
// change's.
func checkReadmeBanner(a *Area) Result {
	const id, f = "scope.readme-banner", "README.md"
	if res, ok := needProfile(a); !ok {
		return res
	}
	if unchanged(a, f) {
		return Result{}
	}
	banner := []byte(a.Profile.Banner)
	has := func(data []byte) (int, bool) {
		i := slices.IndexFunc(splitLines(data), func(l []byte) bool { return bytes.Equal(bytes.TrimSuffix(l, []byte("\r")), banner) })
		return i + 1, i >= 0
	}
	head, atHead, bad := fileAt(a.headTree, f)
	if bad != nil {
		return *bad
	}
	base, atBase, bad := fileAt(a.baseTree, f)
	if bad != nil {
		return *bad
	}
	if _, ok := has(head); ok || (!atHead && !atBase) {
		return Result{}
	}
	n, hadIt := has(base)
	if atBase && !hadIt {
		return Result{}
	}
	ev := newPathEvidence()
	if hadIt {
		ev.line("merge_base", f, n, lineOf(base, n))
		ev.ev.Base = ev.value(a.Profile.Banner)
	}
	return Result{Candidates: []Candidate{ev.candidate(id, f, nil)}}
}

// scope.tooling: a path the change adds that configures a scanner, linter or
// cost tool on the profile's closed list: its base name is one of the listed
// files, or a directory on its path is one of the listed directories. A path
// in no area, such as one under tests/ or wrappers/, is judged in the root.
func checkTooling(a *Area) Result {
	const id = "scope.tooling"
	if res, ok := needProfile(a); !ok {
		return res
	}
	tl := a.Profile.Tooling
	var res Result
	for _, p := range a.headTree.Paths {
		area, _, ok := classify(p)
		if (ok && area != a.Path) || (!ok && a.Path != rootArea) || a.baseTree.Entries[p] != nil || a.baseTree.Dirs[p] {
			continue
		}
		parts := strings.Split(p, "/")
		if !slices.Contains(tl.Files, parts[len(parts)-1]) && !slices.ContainsFunc(parts[:len(parts)-1], func(d string) bool { return slices.Contains(tl.Dirs, d) }) {
			continue
		}
		ev := newPathEvidence()
		ev.ev.Head = ev.value(p)
		res.Candidates = append(res.Candidates, ev.candidate(id, p, nil))
	}
	return res
}

// scope.pre-commit-hook: the set of repository URL and hook id pairs of
// .pre-commit-config.yaml. With the file at the base, a pair the change adds
// or removes is a candidate; when the change creates the file, a pair the
// profile's template lacks is. rev: is not read.
func checkPreCommitHook(a *Area) Result {
	const id, f = "scope.pre-commit-hook", ".pre-commit-config.yaml"
	if res, ok := needProfile(a); !ok {
		return res
	}
	if unchanged(a, f) {
		return Result{}
	}
	head, atHead, bad := fileAt(a.headTree, f)
	if bad != nil {
		return *bad
	}
	base, atBase, bad := fileAt(a.baseTree, f)
	if bad != nil {
		return *bad
	}
	if !atHead && !atBase {
		return Result{}
	}
	ref := base
	if !atBase {
		ref = templateFor(a.Profile, f)
	}
	hp, ok1 := readPreCommit(head)
	rp, ok2 := readPreCommit(ref)
	if !ok1 || !ok2 {
		return Result{NotEvaluated: f + " uses YAML the engine does not read", Code: ReasonUnsupportedFile}
	}
	changed := headChanged(base, head)
	ev := newPathEvidence()
	var line *int
	for _, p := range hp {
		if slices.ContainsFunc(rp, p.same) {
			continue
		}
		if line == nil {
			line = firstChanged(changed, p.idLine, p.repoLine)
			ev.ev.Head = ev.value(p.id)
		}
		ev.line("head", f, p.idLine, lineOf(head, p.idLine))
	}
	if atBase {
		for _, p := range rp {
			if slices.ContainsFunc(hp, p.same) {
				continue
			}
			if ev.ev.Base == nil {
				ev.ev.Base = ev.value(p.id)
			}
			ev.line("merge_base", f, p.idLine, lineOf(base, p.idLine))
		}
	}
	if ev.ev.Head == nil && ev.ev.Base == nil {
		return Result{}
	}
	return Result{Candidates: []Candidate{ev.candidate(id, f, line)}}
}

// hookPair is one repository URL and hook id of a pre-commit configuration,
// with the lines that carry them.
type hookPair struct {
	repo, id         string
	repoLine, idLine int
}

func (p hookPair) same(q hookPair) bool { return p.repo == q.repo && p.id == q.id }

// readPreCommit reads the repository and hook id pairs, in file order. ok is
// false for YAML outside the subset readYAML reads, or a repos or hooks key
// that holds no block list.
func readPreCommit(data []byte) ([]hookPair, bool) {
	ls, ok := readYAML(data)
	if !ok {
		return nil, false
	}
	var out []hookPair
	for i, l := range ls {
		if l.indent != 0 || l.item || l.key != "repos" {
			continue
		}
		repos, ok := listItems(ls, i)
		if !ok {
			return nil, false
		}
		for _, r := range repos {
			rf := fields(ls, r)
			h, has := rf["hooks"]
			if !has {
				continue
			}
			hooks, ok := listItems(ls, h)
			ri, hasRepo := rf["repo"]
			if !ok || !hasRepo {
				return nil, false
			}
			repo := ls[ri]
			for _, k := range hooks {
				hf := fields(ls, k)
				if j, ok := hf["id"]; ok {
					out = append(out, hookPair{repo: repo.val, id: ls[j].val, repoLine: repo.n, idLine: ls[j].n})
				}
			}
		}
	}
	return out, true
}

// scope.profile-file: the profile-owned files. A verbatim file the change
// deletes, a new file in the workflow directory that is not one of the
// profile's workflows, and in a workflow the change edits or deletes, a job
// or step that neither the base nor the template has, or one the base has
// and the head does not, and a uses: whose owner and repository appear in
// neither. In .releaserc.json, the same for top-level keys and plugins.
// Version pins are not read. Byte drift from a template is
// profile.template-drift.
func checkProfileFile(a *Area) Result {
	const id = "scope.profile-file"
	if res, ok := needProfile(a); !ok {
		return res
	}
	po := a.Profile.ProfileOwned
	inWorkflows := func(p string) bool { return strings.HasPrefix(p, po.WorkflowDir+"/") }
	paths := slices.Clone(po.Verbatim)
	for _, t := range []*Tree{a.headTree, a.baseTree} {
		if t != nil {
			paths = append(paths, slices.DeleteFunc(slices.Clone(t.Paths), func(p string) bool { return !inWorkflows(p) })...)
		}
	}
	slices.Sort(paths)
	var res Result
	for _, f := range slices.Compact(paths) {
		if unchanged(a, f) {
			continue
		}
		// Presence is all a deletion or a new file needs, so a link counts
		// as present; only an edit reads the bytes.
		head, atHead, bad := fileAt(a.headTree, f)
		base, atBase, badBase := fileAt(a.baseTree, f)
		ev := newPathEvidence()
		switch {
		case !atHead && atBase:
			ev.ev.Base = ev.value(f)
		case atHead && !atBase:
			if !inWorkflows(f) || slices.Contains(po.Verbatim, f) {
				continue
			}
			ev.ev.Head = ev.value(f)
		case !atHead || (!inWorkflows(f) && f != ".releaserc.json"):
			continue
		case bad != nil:
			return *bad
		case badBase != nil:
			return *badBase
		default:
			line, ok := profileFileEdit(ev, f, head, base, templateFor(a.Profile, f), inWorkflows(f))
			if !ok {
				return Result{NotEvaluated: f + " has content the engine does not read", Code: ReasonUnsupportedFile}
			}
			if ev.ev.Head == nil && ev.ev.Base == nil {
				continue
			}
			res.Candidates = append(res.Candidates, ev.candidate(id, f, line))
			continue
		}
		res.Candidates = append(res.Candidates, ev.candidate(id, f, nil))
	}
	return res
}

// profileItem is one job, step, uses: repository, release key or plugin,
// with the line that names it.
type profileItem struct {
	key  string
	line int
}

// profileFileEdit compares the items of an edited workflow or .releaserc.json
// and fills ev. It returns the line of the first added item the change wrote,
// and ok false when a revision cannot be read.
func profileFileEdit(ev *pathEvidence, f string, head, base, tpl []byte, workflow bool) (*int, bool) {
	read := readReleaserc
	if workflow {
		read = readWorkflow
	}
	h, ok1 := read(head)
	b, ok2 := read(base)
	t, ok3 := read(tpl)
	if tpl == nil {
		t, ok3 = nil, true
	}
	if !ok1 || !ok2 || !ok3 {
		return nil, false
	}
	has := func(items []profileItem, k string) bool {
		return slices.ContainsFunc(items, func(i profileItem) bool { return i.key == k })
	}
	changed := headChanged(base, head)
	var line *int
	for _, i := range h {
		if has(b, i.key) || has(t, i.key) {
			continue
		}
		if ev.ev.Head == nil {
			ev.ev.Head = ev.value(i.key)
			line = firstChanged(changed, i.line)
		}
		ev.line("head", f, i.line, lineOf(head, i.line))
	}
	for _, i := range b {
		// A uses: repository the head no longer names is not itself a
		// removal: the job or step that held it is.
		if strings.HasPrefix(i.key, "uses ") || has(h, i.key) {
			continue
		}
		if ev.ev.Base == nil {
			ev.ev.Base = ev.value(i.key)
		}
		ev.line("merge_base", f, i.line, lineOf(base, i.line))
	}
	return line, true
}

// readWorkflow returns the jobs, steps and uses: repositories of a GitHub
// workflow: "job <id>", "step <job> <identity>" and "uses <owner>/<repo>".
// A step is named by its name, or by its uses: without the version, or its
// run:. ok is false for YAML outside the subset readYAML reads.
func readWorkflow(data []byte) ([]profileItem, bool) {
	ls, ok := readYAML(data)
	if !ok {
		return nil, false
	}
	var out []profileItem
	for i, l := range ls {
		if l.key == "uses" {
			out = append(out, profileItem{"uses " + usesRepo(l.val), l.n})
		}
		if l.indent != 0 || l.item || l.key != "jobs" {
			continue
		}
		if l.val != "" {
			return nil, false
		}
		jobs := fields(ls, i)
		for _, job := range sortedByLine(jobs) {
			out = append(out, profileItem{"job " + ls[job].key, ls[job].n})
			s, has := fields(ls, job)["steps"]
			if !has {
				continue
			}
			steps, ok := listItems(ls, s)
			if !ok {
				return nil, false
			}
			for _, st := range steps {
				sf := fields(ls, st)
				name := ""
				for _, k := range []string{"name", "uses", "run"} {
					if j, ok := sf[k]; ok {
						name = k + " " + ls[j].val
						if k == "uses" {
							name = k + " " + strings.SplitN(ls[j].val, "@", 2)[0]
						}
						break
					}
				}
				out = append(out, profileItem{"step " + ls[job].key + " " + name, ls[st].n})
			}
		}
	}
	return out, true
}

// usesRepo is the owner and repository of a uses: value, or the value
// without its version when it names no repository, such as a local path.
func usesRepo(v string) string {
	v = strings.SplitN(v, "@", 2)[0]
	if strings.HasPrefix(v, ".") || strings.Contains(v, "://") {
		return v
	}
	parts := strings.SplitN(v, "/", 3)
	return strings.Join(parts[:min(2, len(parts))], "/")
}

func sortedByLine(m map[string]int) []int {
	out := make([]int, 0, len(m))
	for _, i := range m {
		out = append(out, i)
	}
	slices.Sort(out)
	return out
}

// readReleaserc returns the top-level keys, "key <name>", and the plugins,
// "plugin <name>", of a .releaserc.json, each with the first line that
// quotes its name. A plugin is its name string or the first element of a
// [name, {options}] array. ok is false for anything else.
func readReleaserc(data []byte) ([]profileItem, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, false
	}
	lineFor := func(name string) int {
		q, _ := json.Marshal(name)
		for i, l := range splitLines(data) {
			if bytes.Contains(l, q) {
				return i + 1
			}
		}
		return 1
	}
	var out []profileItem
	for k := range top {
		out = append(out, profileItem{"key " + k, lineFor(k)})
	}
	if raw, ok := top["plugins"]; ok {
		var plugins []json.RawMessage
		if err := json.Unmarshal(raw, &plugins); err != nil {
			return nil, false
		}
		for _, p := range plugins {
			var name string
			var tuple []json.RawMessage
			if json.Unmarshal(p, &name) != nil {
				if json.Unmarshal(p, &tuple) != nil || len(tuple) == 0 || json.Unmarshal(tuple[0], &name) != nil {
					return nil, false
				}
			}
			out = append(out, profileItem{"plugin " + name, lineFor(name)})
		}
	}
	slices.SortFunc(out, func(x, y profileItem) int { return strings.Compare(x.key, y.key) })
	return out, true
}

// yamlLine is one line of the block-style YAML subset the scope rules read.
// indent is the column of the key; for a list item, of the key after "- ".
type yamlLine struct {
	n, indent int
	item      bool // the line opens a list item
	key, val  string
}

var yamlKey = regexp.MustCompile(`^([A-Za-z0-9_./-]+|"[^"]*"|'[^']*'):(?:\s+(.*))?$`)

// readYAML reads block-style YAML: mappings and lists by indentation, plain
// or quoted single-line scalars and block scalars, whose first content line
// is their value. ok is false for anything else: a tab in the indent, a
// second document, an anchor, alias or tag, a nested list item on one line,
// or a scalar continued on the next line. Flow collections are kept as text.
func readYAML(data []byte) ([]yamlLine, bool) {
	var out []yamlLine
	scalar := -1 // indent of the key whose block scalar is open, or -1
	for i, raw := range splitLines(data) {
		s := strings.TrimSuffix(string(raw), "\r")
		body := strings.TrimLeft(s, " ")
		indent := len(s) - len(body)
		if scalar >= 0 {
			if body == "" || indent > scalar {
				if out[len(out)-1].val == "" && body != "" {
					out[len(out)-1].val = strings.TrimSpace(body)
				}
				continue
			}
			scalar = -1
		}
		switch {
		case body == "" || strings.HasPrefix(body, "#"):
			continue
		case strings.HasPrefix(body, "\t") || strings.Contains(s[:indent], "\t"):
			return nil, false
		case body == "---" && len(out) == 0:
			continue
		}
		l := yamlLine{n: i + 1, indent: indent}
		if body == "-" || strings.HasPrefix(body, "- ") {
			l.item = true
			rest := strings.TrimLeft(strings.TrimPrefix(body, "-"), " ")
			l.indent += len(body) - len(rest)
			body = rest
			if body == "-" || strings.HasPrefix(body, "- ") {
				return nil, false
			}
		}
		m := yamlKey.FindStringSubmatch(body)
		switch {
		case m != nil:
			l.key, l.val = unquote(m[1]), m[2]
		case l.item:
			l.val = body
		default:
			return nil, false
		}
		v, ok := yamlScalar(l.val)
		if !ok {
			return nil, false
		}
		l.val = v
		if v == "|" || v == ">" || strings.HasPrefix(v, "|") || strings.HasPrefix(v, ">") {
			l.val, scalar = "", l.indent
		}
		out = append(out, l)
	}
	return out, true
}

// yamlScalar strips the comment from a single-line scalar and unquotes it.
// ok is false for an anchor, alias or tag, or an unclosed quote.
func yamlScalar(v string) (string, bool) {
	switch {
	case v == "":
		return "", true
	case strings.ContainsAny(v[:1], "&*!"):
		return "", false
	case v[0] == '"' || v[0] == '\'':
		end := strings.IndexByte(v[1:], v[0])
		if end < 0 {
			return "", false
		}
		return v[1 : end+1], true
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v), true
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// under returns the lines nested under line i: for a key, those indented
// past it; for a list item, its own keys too.
func under(ls []yamlLine, i int) []int {
	var out []int
	for j := i + 1; j < len(ls); j++ {
		l := ls[j]
		if l.indent < ls[i].indent || (l.indent == ls[i].indent && (!ls[i].item || l.item)) {
			break
		}
		out = append(out, j)
	}
	return out
}

// fields returns the keys of the mapping at line i, each with its line
// index: for a list item, its own key and the keys beside it; for a key, the
// keys one level under it.
func fields(ls []yamlLine, i int) map[string]int {
	out := map[string]int{}
	at := ls[i].indent
	if ls[i].item {
		if ls[i].key != "" {
			out[ls[i].key] = i
		}
	} else if u := under(ls, i); len(u) > 0 {
		at = ls[u[0]].indent
	}
	for _, j := range under(ls, i) {
		if ls[j].indent == at && !ls[j].item && ls[j].key != "" {
			if _, dup := out[ls[j].key]; !dup {
				out[ls[j].key] = j
			}
		}
	}
	return out
}

// listItems returns the items of the block list under the key at line i. ok
// is false when the key holds a value or a mapping instead.
func listItems(ls []yamlLine, i int) ([]int, bool) {
	u := under(ls, i)
	if ls[i].val != "" || (len(u) > 0 && !ls[u[0]].item) {
		return nil, false
	}
	var out []int
	for _, j := range u {
		if ls[j].item && ls[j].indent == ls[u[0]].indent {
			out = append(out, j)
		}
	}
	return out, true
}
