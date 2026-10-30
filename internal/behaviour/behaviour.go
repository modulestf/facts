package behaviour

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/schema"
)

// Options are the inputs of Run. Exactly one of Base and Diff is set.
type Options struct {
	Head string
	Base string // a base tree
	Diff string // a diff file the base tree is rebuilt from
	// Schema is a `terraform providers schema -json` file. Without it every
	// changed resource argument may be Optional+Computed.
	Schema string
	Limits facts.Limits
}

// classifier holds the inputs of one run.
type classifier struct {
	head, base *facts.Tree
	schema     *schema.Set
	lim        facts.Limits
	views      map[string][2]*view // dir -> base and head view
}

// change is one changed tree entry.
type change struct {
	path, prev string
	kind       string
	h, b       *facts.Entry // nil at the revision without the entry
}

// Run classifies the change from the base to the head tree.
func Run(o Options) (*Report, error) {
	if (o.Base == "") == (o.Diff == "") {
		return nil, errors.New("give exactly one of a base tree and a diff")
	}
	head, err := facts.ReadTree(o.Head, o.Limits)
	if err != nil {
		return nil, fmt.Errorf("head: %w", err)
	}
	var base *facts.Tree
	if o.Base != "" {
		if base, err = facts.ReadTree(o.Base, o.Limits); err != nil {
			return nil, fmt.Errorf("base: %w", err)
		}
	} else if base, _, err = facts.ReadDiffBase(head, o.Diff, o.Limits); err != nil {
		return nil, err
	}
	c := &classifier{head: head, base: base, lim: o.Limits, views: map[string][2]*view{}}
	if o.Schema != "" {
		if c.schema, err = schema.Load("", []string{o.Schema}); err != nil {
			return nil, err
		}
	}
	return c.run()
}

func (c *classifier) run() (*Report, error) {
	changes := diffEntries(c.head, c.base)
	sc, err := scanReadSites([]*facts.Tree{c.base, c.head}, c.lim)
	if err != nil {
		return nil, err
	}
	var specs []spec
	inert := map[string]bool{}
	tfDirs := map[string]bool{}
	for _, ch := range changes {
		switch {
		case ch.kind == KindMode:
			specs = append(specs, spec{file: ch.path, cat: CatUnclassified, kind: KindMode,
				reason: "a mode-only change; the content is the same"})
		case isConfigName(ch.path) || ch.prev != "" && isConfigName(ch.prev):
			tfDirs[path.Dir(ch.path)] = true
			if ch.prev != "" {
				tfDirs[path.Dir(ch.prev)] = true
			}
		case strings.HasSuffix(ch.path, ".tf.json"):
			specs = append(specs, spec{file: ch.path, cat: CatUnclassified, kind: ch.kind,
				reason: "a JSON configuration file is not decomposed"})
		default:
			if s, ok := c.fileChange(ch, sc); ok {
				specs = append(specs, s)
			} else {
				inert[ch.path] = true
			}
		}
	}
	for _, dir := range slices.Sorted(maps.Keys(tfDirs)) {
		specs = append(specs, c.decompose(dir, changes)...)
	}
	specs = append(specs, readTF(changes, specs, sc)...)
	return c.report(changes, specs, inert), nil
}

// fileChange returns the trigger of a changed file that is not a .tf file,
// or false when the entry is inert.
func (c *classifier) fileChange(ch change, sc *scan) (spec, bool) {
	s := spec{file: ch.path, cat: CatNonHCL, kind: ch.kind}
	docKind := func(e *facts.Entry) bool {
		return e == nil || e.Kind != facts.KindSymlink && e.Kind != facts.KindSpecial
	}
	switch {
	case path.Base(ch.path) == ".terraform.lock.hcl":
		s.reason = "the dependency lock file pins provider versions"
	case !facts.InertDocPath(ch.path) || ch.prev != "" && !facts.InertDocPath(ch.prev):
	case !docKind(ch.h) || !docKind(ch.b):
		s.reason = "a documentation path that is not a regular file"
	case !sc.complete:
		s.reason = "the read-site scan is incomplete: a .tf file does not parse or is JSON, or the tree has too many read sites"
	default:
		site, reached := sc.reachedBy(ch.path)
		if !reached && ch.prev != "" {
			site, reached = sc.reachedBy(ch.prev)
		}
		if !reached {
			return spec{}, false
		}
		s.reason = fmt.Sprintf("read by %s:%d", site.File, site.Line)
	}
	return s, true
}

// readTF returns a trigger for each changed .tf file that has no other
// trigger but whose bytes a read site may read, or every one of them when
// the read-site scan is incomplete: its comments and layout are then data.
func readTF(changes []change, specs []spec, sc *scan) []spec {
	has := map[string]bool{}
	for _, s := range specs {
		has[s.file] = true
	}
	var out []spec
	for _, ch := range changes {
		if ch.kind == KindMode || !isConfigName(ch.path) || has[ch.path] || ch.prev != "" && has[ch.prev] {
			continue
		}
		s := spec{file: ch.path, cat: CatNonHCL, kind: ch.kind}
		if !sc.complete {
			s.reason = "the read-site scan is incomplete, so the file's bytes may be read as data"
		} else if site, ok := sc.reachedBy(ch.path); ok {
			s.reason = fmt.Sprintf("read by %s:%d", site.File, site.Line)
		} else if ch.prev != "" {
			if site, ok := sc.reachedBy(ch.prev); ok {
				s.reason = fmt.Sprintf("read by %s:%d", site.File, site.Line)
			}
		}
		if s.reason != "" {
			out = append(out, s)
		}
	}
	return out
}

// isConfigName reports a .tf file Terraform loads.
func isConfigName(p string) bool {
	name := path.Base(p)
	return strings.HasSuffix(name, ".tf") && !ignoredName(name)
}

// decompose returns the triggers of one module directory, or one trigger
// per changed .tf file of it when it cannot be decomposed.
func (c *classifier) decompose(dir string, changes []change) []spec {
	bv, bwhy := buildView(c.base, dir, c.lim)
	hv, hwhy := buildView(c.head, dir, c.lim)
	if why := cmp.Or(hwhy, bwhy); why != "" {
		var out []spec
		for _, ch := range changes {
			if ch.kind != KindMode && isConfigName(ch.path) && (path.Dir(ch.path) == dir || ch.prev != "" && path.Dir(ch.prev) == dir) {
				out = append(out, spec{file: ch.path, cat: CatUnclassified, kind: ch.kind, reason: why})
			}
		}
		return out
	}
	c.views[dir] = [2]*view{bv, hv}
	d := &decomposer{c: c, base: bv, head: hv}
	d.compare()
	return d.out
}

// optionalComputed reports whether a changed resource argument may be
// Optional+Computed, and why. Anything short of a schema that has the
// attribute and says it is not both fails closed to yes.
func (c *classifier) optionalComputed(ctx *resourceCtx, attrPath []string) (bool, string) {
	name := strings.Join(attrPath, ".")
	if c.schema == nil {
		return true, "no provider schema was given, so " + name + " may be Optional and Computed"
	}
	source := ctx.source
	o, f := c.schema.Lookup(schema.Query{Source: source, DataSource: ctx.mode == itemData, Type: ctx.typ, Path: attrPath})
	switch {
	case o == schema.Unavailable:
		return true, "provider " + source + " is not in the schema file, so " + name + " may be Optional and Computed"
	case o == schema.AbsentFromSchema || f.Attribute == nil:
		return true, ctx.typ + "." + name + " is not an attribute in the schema file, so it may be Optional and Computed"
	case f.Attribute.Optional && f.Attribute.Computed:
		return true, "the provider schema marks " + name + " Optional and Computed"
	}
	return false, ""
}

// diffEntries lists the changed entries, sorted by path. A removed and an
// added regular file with the same content are a rename.
func diffEntries(head, base *facts.Tree) []change {
	var out, added, gone []change
	for _, p := range head.Paths {
		h, b := head.Entries[p], base.Entries[p]
		switch {
		case b == nil:
			added = append(added, change{path: p, kind: KindAdded, h: h})
		case h.Kind == facts.KindFile && b.Kind == facts.KindFile && h.SHA256 == b.SHA256:
			if h.Executable() != b.Executable() {
				out = append(out, change{path: p, kind: KindMode, h: h, b: b})
			}
		case !h.Same(b):
			out = append(out, change{path: p, kind: KindModified, h: h, b: b})
		}
	}
	for _, p := range base.Paths {
		if head.Entries[p] == nil {
			gone = append(gone, change{path: p, kind: KindRemoved, b: base.Entries[p]})
		}
	}
	for _, a := range added {
		i := slices.IndexFunc(gone, func(g change) bool {
			return a.h.Kind == facts.KindFile && g.b.Kind == facts.KindFile && a.h.SHA256 == g.b.SHA256
		})
		if i < 0 {
			out = append(out, a)
			continue
		}
		out = append(out, change{path: a.path, prev: gone[i].path, kind: KindRenamed, h: a.h, b: gone[i].b})
		gone = slices.Delete(gone, i, i+1)
	}
	out = append(out, gone...)
	slices.SortFunc(out, func(x, y change) int { return strings.Compare(x.path, y.path) })
	return out
}

// report builds the report: triggers sorted and numbered, at most
// MaxTriggers, and each entry with the triggers in it.
func (c *classifier) report(changes []change, specs []spec, inertFiles map[string]bool) *Report {
	triggers := make([]Trigger, 0, len(specs))
	for _, s := range specs {
		triggers = append(triggers, c.trigger(s))
	}
	slices.SortStableFunc(triggers, compareTriggers)
	triggers = slices.CompactFunc(triggers, func(a, b Trigger) bool { return compareTriggers(a, b) == 0 })
	r := &Report{SchemaVersion: SchemaVersion, Inert: true, Entries: []Entry{}, Triggers: []Trigger{}}
	hasTrigger := map[string]bool{}
	for _, t := range triggers {
		hasTrigger[t.File] = true
	}
	if len(triggers) > MaxTriggers {
		r.Capped = len(triggers) - MaxTriggers
		triggers = triggers[:MaxTriggers]
	}
	ids := map[string][]string{}
	for i := range triggers {
		triggers[i].ID = fmt.Sprintf("T-%03d", i+1)
		ids[triggers[i].File] = append(ids[triggers[i].File], triggers[i].ID)
	}
	r.Triggers = triggers
	for _, ch := range changes {
		e := Entry{Path: ch.path, ChangeKind: ch.kind, TriggerIDs: append([]string{}, ids[ch.path]...)}
		if ch.prev != "" {
			prev := ch.prev
			e.PreviousPath = &prev
			e.TriggerIDs = append(e.TriggerIDs, ids[ch.prev]...)
			slices.Sort(e.TriggerIDs)
		}
		// A .tf entry is inert when the decomposition attributes no change
		// to it, such as a comment or a block moved to another file.
		e.Inert = inertFiles[ch.path] || isConfigName(ch.path) && ch.kind != KindMode &&
			!hasTrigger[ch.path] && (ch.prev == "" || !hasTrigger[ch.prev]) && c.decomposed(ch)
		r.Inert = r.Inert && e.Inert
		r.Entries = append(r.Entries, e)
	}
	return r
}

// decomposed reports whether the directories of a .tf change were
// decomposed, so a change with no trigger there truly has none.
func (c *classifier) decomposed(ch change) bool {
	if _, ok := c.views[path.Dir(ch.path)]; !ok {
		return false
	}
	if ch.prev != "" {
		_, ok := c.views[path.Dir(ch.prev)]
		return ok
	}
	return true
}

// trigger turns a spec into a trigger without its id.
func (c *classifier) trigger(s spec) Trigger {
	t := Trigger{File: s.file, Category: s.cat, ChangeKind: s.kind, ReferencedInputs: []Input{}, ContextualRefs: []string{},
		ForEachOnly: s.forEachOnly}
	if s.line > 0 {
		line := s.line
		t.Line = &line
	}
	if s.addr != "" {
		addr := s.addr
		t.Address = &addr
	}
	if s.attr != "" {
		attr := s.attr
		t.Attribute = &attr
	}
	reason := s.reason
	var okB, okH bool
	t.BaseExpr, okB = capped(s.b)
	t.HeadExpr, okH = capped(s.h)
	if (!okB || !okH) && reason == "" {
		reason = fmt.Sprintf("an expression is over %d bytes and is left out", maxExprBytes)
	}
	inputs, ctx := refs(s.b, s.h)
	v := c.views[path.Dir(s.file)]
	for _, name := range inputs {
		typ := ""
		if v[1] != nil {
			typ = v[1].vars[name]
		}
		if typ == "" && v[0] != nil {
			typ = v[0].vars[name]
		}
		t.ReferencedInputs = append(t.ReferencedInputs, Input{Name: name, Type: cmp.Or(typ, "any")})
	}
	t.ContextualRefs = append(t.ContextualRefs, ctx...)
	t.ContextualRefDetails = c.refDetails(path.Dir(s.file), s.b, s.h)
	t.Pure = (s.b != nil || s.h != nil) && isPure(s.b, s.h)
	t.Levels = []string{}
	switch {
	case s.levels != nil:
		t.Levels = append(t.Levels, s.levels...)
	case !s.noLevels:
		t.Levels = append(t.Levels, levelsOf[s.cat]...)
	}
	if slices.Contains(t.Levels, LevelL1) {
		var why string
		if t.Cases, why = c.cases(t, path.Dir(s.file), s.b, s.h); why != "" {
			reason = strings.TrimPrefix(reason+"; "+why, "; ")
		}
	}
	if reason != "" {
		t.Reason = &reason
	}
	return t
}

func compareTriggers(a, b Trigger) int {
	line := func(t Trigger) int {
		if t.Line == nil {
			return 0
		}
		return *t.Line
	}
	return cmp.Or(
		strings.Compare(a.File, b.File),
		cmp.Compare(line(a), line(b)),
		cmp.Compare(catRank[a.Category], catRank[b.Category]),
		strings.Compare(deref(a.Address), deref(b.Address)),
		strings.Compare(deref(a.Attribute), deref(b.Attribute)),
		strings.Compare(a.ChangeKind, b.ChangeKind),
		strings.Compare(deref(a.BaseExpr), deref(b.BaseExpr)),
		strings.Compare(deref(a.HeadExpr), deref(b.HeadExpr)),
		strings.Compare(deref(a.Reason), deref(b.Reason)),
	)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
