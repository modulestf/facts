package facts

import (
	"bytes"
	"maps"
	"path"
	"slices"
	"strings"
)

// examplesRules is the examples rule family. Rule work for this family adds
// its rules and checks here and in no other rules file; Rules joins every
// family. examples.versions-missing stays in rules.go with the P1 rules.
var examplesRules = []Rule{
	{ID: "examples.disabled-call-missing", Tier: "candidate", Class: ClassConfig, Scope: ScopeAnyFileInArea, Kinds: []string{AreaExample}, Check: checkDisabledCallMissing},
	{ID: "examples.stale-reference", Tier: "candidate", Class: ClassConfig, Scope: ScopeAnyFileInArea, Kinds: []string{AreaExample}, CrossArea: true, NeedsBase: true, Check: checkStaleReference},
	{ID: "examples.submodule-missing", Tier: "candidate", Class: ClassPath, Scope: ScopeNewlyIntroduced, Kinds: []string{AreaSubmodule}, CrossArea: true, NeedsBase: true, Check: checkSubmoduleMissing},
}

// completeExample is the one example the disabled call belongs in.
const completeExample = "examples/complete"

// examples.disabled-call-missing: the complete example has no
// `module "disabled"` call with `create = false`. Only that exact call, with
// the literal false, is clean; any other shape, such as a call by another
// name or a create set from an expression, is a candidate for the confirmer.
// Every other example, and a complete example the change removes, is clean:
// the rule asks nothing of them. The candidate is on the call when there is
// one, and otherwise on main.tf with no line. Its evidence is the call's
// first line at base and at head and its create argument at head, as source
// lines, and `module "disabled"` as base or head where the call exists.
func checkDisabledCallMissing(a *Area) Result {
	if a.Path != completeExample || !a.HeadExists {
		return Result{}
	}
	call := disabledCall(a.Head)
	if call != nil && createIsFalse(call) {
		return Result{}
	}
	const addr = `module "disabled"`
	c := Candidate{
		RuleID: "examples.disabled-call-missing", File: areaFile(a.Path, "main.tf"),
		Subject: subject(a.Path, "module.disabled"), EvidenceComplete: true,
		Evidence: Evidence{SourceLines: []SourceLine{}, SchemaFacts: []SchemaFact{}},
	}
	if a.Base != nil {
		if b := disabledCall(a.Base); b != nil {
			s := addr
			c.Evidence.Base = &s
			c.addLine("merge_base", a.baseTree, areaFile(a.Path, b.File), b.Line)
		}
	}
	if call != nil {
		s, line := addr, call.Line
		c.File, c.Line, c.Evidence.Head = areaFile(a.Path, call.File), &line, &s
		lines := []int{call.Line}
		for _, at := range call.Body.Attributes {
			if at.Name == "create" && at.Range.Start.Line != call.Line {
				lines = append(lines, at.Range.Start.Line)
			}
		}
		for _, n := range lines {
			c.addLine("head", a.headTree, c.File, n)
		}
	}
	return Result{Candidates: []Candidate{c}}
}

// addLine adds line n of a file of a tree to a candidate's source lines,
// capped at maxEvidenceText. A line that cannot be read is left out.
func (c *Candidate) addLine(revision string, t *Tree, file string, n int) {
	text, ok := treeLine(t, file, n)
	if !ok {
		return
	}
	if len(text) > maxEvidenceText {
		text, c.EvidenceComplete = text[:maxEvidenceText], false
	}
	c.Evidence.SourceLines = append(c.Evidence.SourceLines, SourceLine{Revision: revision, File: file, Line: n, Text: text})
}

// disabledCall returns the first module call named disabled, or nil.
func disabledCall(m *Module) *ModuleCall {
	for i := range m.ModuleCalls {
		if m.ModuleCalls[i].Name == "disabled" {
			return &m.ModuleCalls[i]
		}
	}
	return nil
}

// createIsFalse reports whether a call sets create to the literal false.
func createIsFalse(c *ModuleCall) bool {
	for _, at := range c.Body.Attributes {
		if at.Name == "create" {
			return strings.TrimSpace(at.Value.Text) == "false"
		}
	}
	return false
}

// headLine returns line n of a regular head file of the area, without its
// line ending.
func headLine(a *Area, file string, n int) (string, bool) {
	return treeLine(a.headTree, areaFile(a.Path, file), n)
}

// treeLine returns line n of a regular file of a tree, without its line
// ending. A nil tree has no lines.
func treeLine(t *Tree, p string, n int) (string, bool) {
	if t == nil {
		return "", false
	}
	e := t.Entries[p]
	if e == nil || e.Kind != KindFile {
		return "", false
	}
	ls := splitLines(e.Data)
	if n < 1 || n > len(ls) {
		return "", false
	}
	return string(bytes.TrimRight(ls[n-1], "\r")), true
}

// examples.submodule-missing: a submodule the change adds has no example of
// its own. An example directory of the same name, examples/<name>, is its
// own example and the rule is clean; with none the submodule is a candidate,
// since another example may still call it. A submodule that is not new, or
// that the change removes, is clean. The rule reads paths only, and the
// evidence: the submodule path as head, null base, and the lines of example
// .tf files that name modules/<name>, at most maxEvidenceComments of them.
func checkSubmoduleMissing(a *Area) Result {
	if a.BaseExists || !a.HeadExists {
		return Result{}
	}
	name := path.Base(a.Path)
	if a.headTree.Dirs[path.Join("examples", name)] {
		return Result{}
	}
	head := a.Path
	c := Candidate{
		RuleID: "examples.submodule-missing", File: a.Path, Subject: a.Path, EvidenceComplete: true,
		Evidence: Evidence{SourceLines: []SourceLine{}, Head: &head, SchemaFacts: []SchemaFact{}},
	}
	needle := []byte(a.Path)
	for _, p := range a.headTree.Paths {
		e := a.headTree.Entries[p]
		if !strings.HasPrefix(p, "examples/") || !strings.HasSuffix(p, ".tf") || e.Kind != KindFile {
			continue
		}
		for i, l := range splitLines(e.Data) {
			if !referencesPath(l, needle) {
				continue
			}
			if len(c.Evidence.SourceLines) == maxEvidenceComments {
				c.EvidenceComplete = false
				return Result{Candidates: []Candidate{c}}
			}
			text := string(bytes.TrimRight(l, "\r"))
			if len(text) > maxEvidenceText {
				text, c.EvidenceComplete = text[:maxEvidenceText], false
			}
			c.Evidence.SourceLines = append(c.Evidence.SourceLines, SourceLine{Revision: "head", File: p, Line: i + 1, Text: text})
		}
	}
	return Result{Candidates: []Candidate{c}}
}

// referencesPath reports whether a line names the module path p whole: p
// followed by a quote, a slash or the end of the line, so modules/extra does
// not match modules/extra2.
func referencesPath(l, p []byte) bool {
	for i := bytes.Index(l, p); i >= 0; {
		end := i + len(p)
		if end == len(l) || l[end] == '"' || l[end] == '/' {
			return true
		}
		j := bytes.Index(l[end:], p)
		if j < 0 {
			return false
		}
		i = end + j
	}
	return false
}

// staleRef is one input or output of a called module that the change
// removed or renamed: where its candidate is anchored and what it is.
type staleRef struct {
	subject string // "<callee>:output.<name>" or "<callee>:var.<name>"
	file    string // the file the change edits
	line    *int   // the renamed block's head line; nil for a removal
	base    string // the address at base
	// baseFile and baseLine are where the block was at base.
	baseFile string
	baseLine int
	head     *string
}

// examples.stale-reference: an example reads an output, or sets an input, of
// a module in this repository that the change removed or renamed. The
// candidate is anchored at the change that causes it, in the called module:
// a removal on the file that held the block, with no line; a rename on the
// head line of the new output. An output is renamed when a new output has
// the removed one's value as written; an input is never taken as renamed.
// The evidence is the block's first line at base and every line of the
// examples that reads it, at most maxEvidenceComments, so the confirmer sees
// both sides. Each reference gives one candidate, from the
// first example by path that reads it.
//
// An output read is `module.<call>.<name>` in an example's .tf text. Any
// other use of `module.<call>`, such as an index or the whole object, may
// read any output, so it counts as a read of every removed output. Text in
// comments counts too; the confirmer rejects it. A call whose source is not
// a local path to a module area calls no code of this change.
func checkStaleReference(a *Area) Result {
	if !a.HeadExists {
		return Result{}
	}
	reads := map[string][]SourceLine{}
	refs := map[string]staleRef{}
	first := map[string]string{} // subject -> path of the first example that reads it
	paths := make([]string, 0, len(a.peers))
	for p, peer := range a.peers {
		if peer.Kind == AreaExample && peer.HeadExists && peer.Issue == "" && len(peer.Head.Unsupported) == 0 {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	for _, p := range paths {
		ex := a.peers[p]
		for _, call := range ex.Head.ModuleCalls {
			callee := calledArea(ex, call)
			if callee == nil {
				continue
			}
			if reason, code := moduleIssue(callee); reason != "" {
				return Result{NotEvaluated: reason, Code: code}
			}
			for _, r := range staleReads(ex, call, callee) {
				if _, ok := first[r.ref.subject]; !ok {
					first[r.ref.subject] = p
				}
				refs[r.ref.subject] = r.ref
				reads[r.ref.subject] = append(reads[r.ref.subject], r.line)
			}
		}
	}
	var res Result
	for _, subj := range slices.Sorted(maps.Keys(refs)) {
		if first[subj] != a.Path {
			continue
		}
		ref := refs[subj]
		base := ref.base
		c := Candidate{
			RuleID: "examples.stale-reference", File: ref.file, Line: ref.line, Subject: subj, EvidenceComplete: true,
			Evidence: Evidence{SourceLines: []SourceLine{}, Base: &base, Head: ref.head, SchemaFacts: []SchemaFact{}},
		}
		// The block at base first, then the example lines that read it.
		c.addLine("merge_base", a.baseTree, ref.baseFile, ref.baseLine)
		for i, l := range reads[subj] {
			if i == maxEvidenceComments {
				c.EvidenceComplete = false
				break
			}
			if len(l.Text) > maxEvidenceText {
				l.Text, c.EvidenceComplete = l.Text[:maxEvidenceText], false
			}
			c.Evidence.SourceLines = append(c.Evidence.SourceLines, l)
		}
		res.Candidates = append(res.Candidates, c)
	}
	return res
}

// calledArea returns the root or submodule area a module call's local
// source names, or nil.
func calledArea(ex *Area, call ModuleCall) *Area {
	if !strings.HasPrefix(call.Source, "./") && !strings.HasPrefix(call.Source, "../") {
		return nil
	}
	callee := ex.peers[path.Join(ex.Path, call.Source)]
	if callee == nil || (callee.Kind != AreaRoot && callee.Kind != AreaSubmodule) {
		return nil
	}
	return callee
}

// moduleIssue is why a called module's interface cannot be read at head or
// base, with its reason code, or "".
func moduleIssue(m *Area) (reason, code string) {
	switch {
	case m.Issue != "":
		return m.Issue, m.issueCode
	case len(m.Head.Unsupported) > 0:
		return m.Head.Unsupported[0], m.Head.reasonCode()
	case m.Base != nil && len(m.Base.Unsupported) > 0:
		return "at base: " + m.Base.Unsupported[0], m.Base.reasonCode()
	}
	return "", ""
}

type staleRead struct {
	ref  staleRef
	line SourceLine
}

// staleReads returns the reads by one example's call of the inputs and
// outputs the change removed or renamed in the called module.
func staleReads(ex *Area, call ModuleCall, callee *Area) []staleRead {
	if callee.Base == nil {
		return nil
	}
	var out []staleRead
	for _, at := range call.Body.Attributes {
		if at.Name == "source" || at.Name == "version" || !removedVariable(callee, at.Name) {
			continue
		}
		v := callee.Base.Variables[slices.IndexFunc(callee.Base.Variables, func(v Variable) bool { return v.Name == at.Name })]
		file := areaFile(ex.Path, call.File)
		text, _ := headLine(ex, call.File, at.Range.Start.Line)
		out = append(out, staleRead{
			ref: staleRef{subject: subject(callee.Path, "var."+v.Name), file: areaFile(callee.Path, v.File), base: "var." + v.Name,
				baseFile: areaFile(callee.Path, v.File), baseLine: v.Line},
			line: SourceLine{Revision: "head", File: file, Line: at.Range.Start.Line, Text: text},
		})
	}
	removed := removedOutputs(callee)
	if len(removed) == 0 {
		return out
	}
	prefix := []byte("module." + call.Name)
	for _, p := range ex.headTree.Paths {
		e := ex.headTree.Entries[p]
		if path.Dir(p) != ex.Path || !strings.HasSuffix(p, ".tf") || e.Kind != KindFile {
			continue
		}
		for i, l := range splitLines(e.Data) {
			for _, o := range outputsRead(l, prefix, removed) {
				out = append(out, staleRead{
					ref:  outputRef(callee, o),
					line: SourceLine{Revision: "head", File: p, Line: i + 1, Text: string(bytes.TrimRight(l, "\r"))},
				})
			}
		}
	}
	return out
}

func removedVariable(m *Area, name string) bool {
	return m.Base.varNames[name] && !m.Head.varNames[name]
}

// removedOutputs are the base outputs with no head output of their name.
func removedOutputs(m *Area) []Output {
	var out []Output
	for _, o := range m.Base.Outputs {
		if !m.Head.outNames[o.Name] {
			out = append(out, o)
		}
	}
	return out
}

// outputRef anchors a removed output: on the head line of a new output with
// the same value as written, a rename, and otherwise on the file that held
// it, with no line.
func outputRef(m *Area, o Output) staleRef {
	ref := staleRef{subject: subject(m.Path, "output."+o.Name), file: areaFile(m.Path, o.File), base: "output." + o.Name,
		baseFile: areaFile(m.Path, o.File), baseLine: o.Line}
	for _, h := range m.Head.Outputs {
		if !m.Base.outNames[h.Name] && h.Value != nil && o.Value != nil && h.Value.Text == o.Value.Text {
			line, head := h.Line, "output."+h.Name
			ref.file, ref.line, ref.head = areaFile(m.Path, h.File), &line, &head
			break
		}
	}
	return ref
}

// outputsRead returns the removed outputs a line reads through
// module.<call>: each `module.<call>.<name>`, and every one for any other
// use of module.<call>.
func outputsRead(l, prefix []byte, removed []Output) []Output {
	var out []Output
	for i := 0; ; {
		j := bytes.Index(l[i:], prefix)
		if j < 0 {
			return out
		}
		start, end := i+j, i+j+len(prefix)
		i = end
		if start > 0 && (isIdentByte(l[start-1]) || l[start-1] == '.') || end < len(l) && isIdentByte(l[end]) {
			continue // part of a longer name
		}
		if end < len(l) && l[end] == '.' {
			k := end + 1
			for k < len(l) && isIdentByte(l[k]) {
				k++
			}
			name := string(l[end+1 : k])
			for _, o := range removed {
				if o.Name == name && !slices.ContainsFunc(out, func(x Output) bool { return x.Name == name }) {
					out = append(out, o)
				}
			}
			continue
		}
		for _, o := range removed {
			if !slices.ContainsFunc(out, func(x Output) bool { return x.Name == o.Name }) {
				out = append(out, o)
			}
		}
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '-' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
