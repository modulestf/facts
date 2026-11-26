package gates

import (
	"os"
	"strings"
)

// Laptop mode: the strict gates, and the relaxed data source and provider
// flag rules, which a banned file name anywhere in scope turns off.

// allowedData are the data sources a laptop run reads at any time.
var allowedData = map[string]bool{"aws_caller_identity": true, "aws_canonical_user_id": true, "aws_partition": true,
	"aws_region": true, "aws_availability_zones": true, "aws_iam_policy_document": true, "aws_service_principal": true,
	"aws_default_tags": true, "tls_public_key": true, "cloudinit_config": true}

// freeData make no API call; they pass when no banned name is in scope.
var freeData = map[string]bool{"aws_arn": true, "aws_cloudwatch_log_data_protection_policy_document": true}

// regionFree are data sources whose region goes nowhere.
var regionFree = map[string]bool{"aws_region": true, "aws_service_principal": true}

// topWords are the blocks a file may hold at its top level.
var topWords = map[string]bool{"terraform": true, "provider": true, "variable": true, "locals": true, "output": true,
	"resource": true, "data": true, "module": true, "moved": true, "removed": true}

// typePrefixes are the resource and data source types of the allowed
// providers.
var typePrefixes = []string{"aws_", "random_", "time_", "tls_", "cloudinit_"}

type blk struct {
	labels    []string
	s, e, hdr int // body start and end, header start
}

// blocks lists the blocks of keyword kw with at least one quoted label.
func blocks(m string, mt *text, raw, kw string) []blk {
	var out []blk
	at := atBlockHeader(kw)
	for x := findWord(m, 0, kw, at); x.start >= 0; {
		labels := quotedLabels(raw[x.g0:x.g1])
		e := mt.block(x.end)
		if e < 0 {
			x = findWord(m, x.end, kw, at)
			continue
		}
		out = append(out, blk{labels, x.end + 1, e - 1, x.start})
		x = findWord(m, e, kw, at)
	}
	return out
}

func label0(b blk) string {
	if len(b.labels) > 0 {
		return b.labels[0]
	}
	return ""
}

// tokAt reads the value in m[s:e] as the Perl's tok does; a string's text
// is taken from the raw file.
func tokAt(m, raw string, s, e int) *tok {
	a := skipBlank(m[:e], s)
	b := e
	for b > a && isBlank(m[b-1]) {
		b--
	}
	t := m[a:b]
	if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' && strings.Trim(t[1:len(t)-1], " ") == "" {
		c := raw[a+1 : b-1]
		if strings.Contains(c, "${") || strings.Contains(c, "%{") {
			return &tok{kind: "other"}
		}
		return &tok{kind: "str", val: c}
	}
	switch t {
	case "null":
		return &tok{kind: "null"}
	case "true", "false":
		return &tok{kind: "bool", val: t}
	}
	for _, k := range []string{"var", "local"} {
		if n, ok := strings.CutPrefix(t, k+"."); ok {
			if identEnd(n, 0) == len(n) {
				return &tok{kind: k, val: n}
			}
		}
	}
	return &tok{kind: "other"}
}

// attrHead is \A[ \t]*([A-Za-z_][\w-]*)[ \t]*=[ \t]* on one line: the name
// and the value's offset.
func attrHead(l string) (string, int, bool) {
	i := skipBlank(l, 0)
	ne := identEnd(l, i)
	if ne < 0 {
		return "", 0, false
	}
	j := skipBlank(l, ne)
	if j >= len(l) || l[j] != '=' {
		return "", 0, false
	}
	return l[i:ne], skipBlank(l, j+1), true
}

// topAttrs is the Perl's top_attrs: each argument of a body at its own
// depth, nested blocks filled, and the names set twice.
func topAttrs(m, raw string, s, e int) (map[string]*tok, map[string]bool) {
	a, dup := map[string]*tok{}, map[string]bool{}
	p := s
	for l := range strings.SplitSeq(fillBlocks(m[s:e]), "\n") {
		if name, off, ok := attrHead(l); ok {
			if _, seen := a[name]; seen {
				dup[name] = true
			}
			a[name] = tokAt(m, raw, p+off, p+len(l))
		}
		p += len(l) + 1
	}
	return a, dup
}

type bodyLine struct {
	name   string
	vs, ve int
}

// bodyLines is the Perl's body_lines: each line of m[s:e] that is not blank
// as name = value, or false when one is not.
func bodyLines(m string, s, e int) ([]bodyLine, bool) {
	var out []bodyLine
	p := s
	for l := range strings.SplitSeq(m[s:e], "\n") {
		if hasNonSpace(l) {
			name, off, ok := attrHead(l)
			if !ok || off >= len(l) || isSpace(l[off]) {
				return nil, false
			}
			out = append(out, bodyLine{name, p + off, p + len(l)})
		}
		p += len(l) + 1
	}
	return out, true
}

// policyARN is the Perl's $ARNOK: an AWS managed policy ARN, its partition
// a literal or data.aws_partition. It returns the policy path.
func policyARN(v string) (string, bool) {
	rest, ok := strings.CutPrefix(v, "arn:")
	if !ok {
		return "", false
	}
	switch {
	case strings.HasPrefix(rest, "aws"):
		i := 3
		for i < len(rest) && rest[i] == '-' {
			j := i + 1
			for j < len(rest) && rest[j] >= 'a' && rest[j] <= 'z' {
				j++
			}
			if j == i+1 {
				break
			}
			i = j
		}
		rest = rest[i:]
	case strings.HasPrefix(rest, "${data.aws_partition."):
		i := len("${data.aws_partition.")
		e := identEnd(rest, i)
		if e < 0 || !hasAt(rest, e, ".partition}") {
			return "", false
		}
		rest = rest[e+len(".partition}"):]
	default:
		return "", false
	}
	p, ok := strings.CutPrefix(rest, ":iam::aws:policy/")
	if !ok {
		return "", false
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "" {
			return "", false
		}
		for i := 0; i < len(seg); i++ {
			if c := seg[i]; !isWord(c) && !strings.ContainsRune("+=,.@-", rune(c)) {
				return "", false
			}
		}
	}
	return p, true
}

// policyOK is the Perl's policy_ok: a data aws_iam_policy whose body is one
// managed policy ARN and at most one count.
func policyOK(m, raw string, s, e int) bool {
	lines, ok := bodyLines(m, s, e)
	if !ok {
		return false
	}
	n := map[string]int{}
	for _, l := range lines {
		n[l.name]++
		v := m[l.vs:l.ve]
		if l.name == "count" {
			if strings.Contains(v, "<<") {
				return false
			}
			continue
		}
		if l.name != "arn" || len(v) < 2 || v[0] != '"' {
			return false
		}
		q := strings.IndexByte(v[1:], '"')
		if q < 0 || strings.Trim(v[1:1+q], " ") != "" || skipBlank(v, q+2) != len(v) {
			return false
		}
		p, ok := policyARN(raw[l.vs+1 : l.vs+1+q])
		if !ok || len(p) > 512 {
			return false
		}
		for seg := range strings.SplitSeq(p, "/") {
			if seg == "." || seg == ".." {
				return false
			}
		}
	}
	return n["arn"] == 1 && n["count"] <= 1
}

// nameOK is \A[A-Za-z0-9._-]{1,256}\z.
func nameOK(s string) bool {
	if len(s) < 1 || len(s) > 256 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !isWord(c) && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

// regionName is \A[a-z]{2}(?:-[a-z]+)+-[0-9]{1,2}\z.
func regionName(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) < 3 || len(parts[0]) != 2 || !lower(parts[0]) {
		return false
	}
	for _, p := range parts[1 : len(parts)-1] {
		if p == "" || !lower(p) {
			return false
		}
	}
	d := parts[len(parts)-1]
	return len(d) >= 1 && len(d) <= 2 && allDigits(d)
}

func lower(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'a' || s[i] > 'z' {
			return false
		}
	}
	return true
}

// rl reads local.NAME as the bare literal it is declared once as in
// directory d, or nil.
func (r *run) rl(t *tok, d string, dok bool) *tok {
	if t.kind != "local" {
		return t
	}
	if r.ban || r.lall || !dok || r.lbad[d] {
		return nil
	}
	v := r.loc[d][t.val]
	if v == nil || v.n != 1 || !lit(v.t) {
		return nil
	}
	return v.t
}

// eff is the value var.NAME of module directory d takes in call c, or nil
// when it cannot be known.
func (r *run) eff(t *tok, c *call, d string) *tok {
	if t == nil || t.kind != "var" {
		return t
	}
	n := t.val
	v := r.vars[d][n]
	if v == nil || v.dup || c.dup[n] {
		return nil
	}
	nl, df := v.nullable, v.def
	if nl != nil && nl.kind != "bool" {
		return nil
	}
	var dflt *tok
	if lit(df) {
		dflt = df
	}
	in, set := c.attrs[n]
	if !set {
		return dflt
	}
	x := r.rl(in, c.from, true)
	if x == nil {
		return nil
	}
	switch x.kind {
	case "str", "bool":
		return x
	case "null":
		if nl != nil && nl.val == "false" {
			return dflt
		}
		return &tok{kind: "null"}
	}
	return nil
}

func busPass(nm, rg *tok) bool {
	return nm != nil && rg != nil && nm.kind == "str" && nameOK(nm.val) &&
		(rg.kind == "null" || rg.kind == "str" && regionName(rg.val))
}

func regionVal(t *tok) bool {
	return t != nil && (t.kind == "null" || t.kind == "str" && regionName(t.val))
}

// callsInto are the module calls whose target is d.
func (r *run) callsInto(d string) []*call {
	var out []*call
	for _, c := range r.calls {
		if c.to == d {
			out = append(out, c)
		}
	}
	return out
}

// regionOK is the Perl's region_ok: the region argument of a data block is
// absent, a literal region or null, or a variable every call from the
// example sets to one.
func (r *run) regionOK(f, m, raw string, s, e int) bool {
	a, dup := topAttrs(m, raw, s, e)
	if dup["region"] {
		return false
	}
	rt, set := a["region"]
	if !set {
		return true
	}
	d, dok := realpath(dirOf(f))
	t := r.rl(rt, d, dok)
	if t == nil {
		return false
	}
	if lit(t) {
		return regionVal(t)
	}
	if !r.a3 || t.kind != "var" || !dok || d == r.ex {
		return false
	}
	cs := r.callsInto(d)
	if len(cs) == 0 {
		return false
	}
	for _, c := range cs {
		if c.from != r.ex || !regionVal(r.eff(t, c, d)) {
			return false
		}
	}
	return true
}

// busOK is the Perl's bus_ok: a data aws_cloudwatch_event_bus whose name
// and region are literals, or variables every call from the example sets.
func (r *run) busOK(f, m, raw string, s, e int) bool {
	if !r.a3 {
		return false
	}
	lines, ok := bodyLines(m, s, e)
	if !ok {
		return false
	}
	n, t := map[string]int{}, map[string]*tok{}
	for _, l := range lines {
		n[l.name]++
		switch l.name {
		case "count":
			if strings.Contains(m[l.vs:l.ve], "<<") {
				return false
			}
		case "name", "region":
			t[l.name] = tokAt(m, raw, l.vs, l.ve)
		default:
			return false
		}
	}
	if n["name"] != 1 || n["count"] > 1 || n["region"] > 1 {
		return false
	}
	if t["region"] == nil {
		t["region"] = &tok{kind: "null"}
	}
	d, dok := realpath(dirOf(f))
	if !dok {
		return false
	}
	for k, v := range t {
		if t[k] = r.rl(v, d, true); t[k] == nil {
			return false
		}
	}
	if d == r.ex {
		return lit(t["name"]) && lit(t["region"]) && busPass(t["name"], t["region"])
	}
	for _, v := range t {
		if !lit(v) && v.kind != "var" {
			return false
		}
	}
	cs := r.callsInto(d)
	if len(cs) == 0 {
		return false
	}
	for _, c := range cs {
		if c.from != r.ex || !busPass(r.eff(t["name"], c, d), r.eff(t["region"], c, d)) {
			return false
		}
	}
	return true
}

// prepare is the laptop pass before the gates: masks, module calls,
// variables, locals and the ban.
func (r *run) prepare(files []file) {
	r.masks = map[string]*string{}
	r.vars = map[string]map[string]*variable{}
	r.dirs = map[string]bool{}
	r.loc = map[string]map[string]*local{}
	r.lbad = map[string]bool{}
	r.a3 = !r.o.Downloaded && r.hasEx
	for _, f := range files {
		rd, rdok := realpath(dirOf(f.path))
		if !f.ok {
			r.a3 = false
			if rdok {
				r.lbad[rd] = true
			} else {
				r.lall = true
			}
			continue
		}
		if !rdok {
			r.ban = true
			continue
		}
		r.dirs[rd] = true
		m, ok := mask(f.raw)
		if !ok {
			r.masks[f.path] = nil
			r.a3 = false
			r.lbad[rd] = true
			continue
		}
		r.masks[f.path] = &m
		// A dot file is gated but never read for calls, variables or
		// locals: Terraform ignores it.
		if strings.HasPrefix(baseOf(f.path), ".") {
			continue
		}
		r.readDecls(rd, m, f.raw)
	}
	for d := range r.dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			r.ban = true
			continue
		}
		for _, e := range ents {
			if banName(e.Name()) {
				r.ban = true
			}
		}
	}
	if r.ban {
		r.a3 = false
	}
}

// readDecls reads the locals, module calls and variables of one file of
// directory rd.
func (r *run) readDecls(rd, m, raw string) {
	mt := newText(m)
	for x := findWord(m, 0, "locals", atLocals); x.start >= 0; {
		e := mt.block(x.end)
		if e < 0 {
			x = findWord(m, x.end, "locals", atLocals)
			continue
		}
		a, dup := topAttrs(m, raw, x.end+1, e-1)
		if r.loc[rd] == nil {
			r.loc[rd] = map[string]*local{}
		}
		for k, v := range a {
			l := r.loc[rd][k]
			if l == nil {
				l = &local{}
				r.loc[rd][k] = l
			}
			if dup[k] {
				l.n += 2
			} else {
				l.n++
			}
			l.t = v
		}
		x = findWord(m, e, "locals", atLocals)
	}
	for _, b := range blocks(m, mt, raw, "module") {
		a, dup := topAttrs(m, raw, b.s, b.e)
		st := a["source"]
		if st == nil || dup["source"] || st.kind != "str" {
			r.a3 = false
			continue
		}
		s := st.val
		if localSource(s) {
			t, ok := realpath(rd + "/" + s)
			if !ok || !isDir(t) {
				r.a3 = false
				continue
			}
			r.dirs[t] = true
			if !under(t, r.root) {
				r.a3 = false
			}
			r.calls = append(r.calls, &call{rd, t, a, dup})
		} else if !moduleSource(s) {
			r.a3 = false
		}
	}
	for _, b := range blocks(m, mt, raw, "variable") {
		a, dup := topAttrs(m, raw, b.s, b.e)
		n := label0(b)
		if r.vars[rd] == nil {
			r.vars[rd] = map[string]*variable{}
		}
		_, seen := r.vars[rd][n]
		r.vars[rd][n] = &variable{def: a["default"], nullable: a["nullable"],
			dup: seen || len(b.labels) != 1 || dup["default"] || dup["nullable"]}
	}
}

// relaxed is one relaxed data source type of a file: whether every block
// of it passed its rule, and how many there are.
type relaxed struct {
	ok bool
	n  int
}

// laptopFile runs the laptop gates of one file.
func (r *run) laptopFile(f, raw string) {
	mp := r.masks[f]
	rel := map[string]*relaxed{}
	if mp != nil {
		m := *mp
		mt := newText(m)
		data := blocks(m, mt, raw, "data")
		if !r.ban {
			for _, b := range data {
				y := label0(b)
				if len(b.labels) != 2 || y != "aws_iam_policy" && y != "aws_cloudwatch_event_bus" {
					continue
				}
				var ok bool
				if y == "aws_iam_policy" {
					ok = policyOK(m, raw, b.s, b.e)
				} else {
					ok = r.busOK(f, m, raw, b.s, b.e)
				}
				if rel[y] == nil {
					rel[y] = &relaxed{ok: true}
				}
				rel[y].ok = rel[y].ok && ok
				rel[y].n++
			}
		}
		// The region argument of every data block (plan-pass.md, Data source regions)
		for _, b := range data {
			if len(b.labels) == 2 && regionFree[b.labels[0]] {
				continue
			}
			if !r.regionOK(f, m, raw, b.s, b.e) {
				r.hit("G3", f, m, b.hdr)
			}
		}
	} else if findWord(raw, 0, "data", atDataQuote).start >= 0 {
		r.hitFile("G3", f)
	}
	code := stripComments(blank(raw))
	if i := firstNonASCII(code); i >= 0 {
		r.hit("G0", f, code, i)
	}
	nc := stripComments(raw)
	for x := findLines(nc, 0, atLabelLine); x.start >= 0; x = findLines(nc, x.end, atLabelLine) {
		if firstNonASCII(nc[x.g0:x.g1]) >= 0 {
			r.hit("G0", f, nc, x.g0)
		}
	}
	if i := strings.Index(code, "/*"); i >= 0 {
		r.hit("G0", f, code, i)
	}
	if i := findADJ(code); i >= 0 {
		r.hit("G3", f, code, i)
	}
	for x := findFrom(code, 0, atKW); x.start >= 0; x = findFrom(code, x.end, atKW) {
		r.hit("G3", f, code, x.start)
	}
	for _, t := range []string{raw, nc} {
		r.laptopText(f, t, mp != nil, rel)
	}
	if r.o.Downloaded {
		return
	}
	r.laptopClone(f, raw)
}

// atDataQuote is \bdata\s+" at i.
func atDataQuote(s string, i int) (span, bool) {
	if !hasAt(s, i, "data") || !boundary(s, i) {
		return none, false
	}
	k := skipSpace(s, i+4)
	if k == i+4 || k >= len(s) || s[k] != '"' {
		return none, false
	}
	return span{start: i, end: k + 1}, true
}

// laptopText runs the text rules over t, the raw file or the file with its
// comments removed.
func (r *run) laptopText(f, t string, masked bool, rel map[string]*relaxed) {
	for x := findFrom(t, 0, atCode); x.start >= 0; x = findFrom(t, x.end, atCode) {
		r.hit("G3", f, t, x.start)
	}
	for x := findLines(t, 0, atTopLine); x.start >= 0; x = findLines(t, x.end, atTopLine) {
		if !topWords[t[x.g0:x.g1]] {
			r.hit("G3", f, t, x.start)
		}
	}
	for x := findSpaceLines(t, 0, atNewBlock); x.start >= 0; x = findSpaceLines(t, x.end, atNewBlock) {
		r.hit("G3", f, t, x.start)
	}
	seen := map[string]int{}
	for x := findFrom(t, 0, atTypeRef); x.start >= 0; x = findFrom(t, x.end, atTypeRef) {
		if hasAt(t, x.start, "data") {
			seen[t[x.g0:x.g1]]++
		}
	}
	for x := findFrom(t, 0, atTypeRef); x.start >= 0; x = findFrom(t, x.end, atTypeRef) {
		y := t[x.g0:x.g1]
		ok := false
		for _, p := range typePrefixes {
			if strings.HasPrefix(y, p) {
				ok = true
			}
		}
		// A relaxed type passes only when every mention of it is a block that passed its rule
		rx := rel[y] != nil && rel[y].ok && rel[y].n == seen[y]
		if hasAt(t, x.start, "data") && !allowedData[y] && (r.ban || !masked || !freeData[y]) && !rx {
			ok = false
		}
		if !ok {
			r.hit("G3", f, t, x.start)
		}
	}
	r.laptopProviders(f, t)
}

// laptopProviders allows a provider block of region, alias, default_tags
// and the documented skip flags only.
func (r *run) laptopProviders(f, t string) {
	bt := blank(t)
	btt := newText(bt)
	header := atHeader("provider")
	for x := findWord(bt, 0, "provider", header); x.start >= 0; {
		at, next := x.start, x.end
		e := btt.block(x.end)
		if e < 0 {
			r.hit("G3", f, bt, at)
			x = findWord(bt, next, "provider", header)
			continue
		}
		next = e
		b0 := x.end + 1
		body0 := bt[b0 : e-1]
		if strings.Contains(body0, "<<") {
			r.hit("G3", f, bt, at)
			x = findWord(bt, next, "provider", header)
			continue
		}
		tags := atDefaultTags(newText(body0))
		body := replaceAll(body0, "default_tags", tags, func(string) string { return "" })
		bf := replaceAll(body0, "default_tags", tags, fill)
		// skip_region_validation = true only beside one region set to a bare literal region name;
		// the values are read from the text before strings were filled, at the same offsets
		var rg []string
		off := 0
		for l := range strings.SplitSeq(bf, "\n") {
			if v0, v1, ok := regionLine(l); ok {
				rg = append(rg, t[b0+off+v0:b0+off+v1])
			}
			off += len(l) + 1
		}
		rlit := false
		if len(rg) == 1 && len(rg[0]) >= 2 && rg[0][0] == '"' && rg[0][len(rg[0])-1] == '"' {
			in := rg[0][1 : len(rg[0])-1]
			rlit = !strings.Contains(in, `"`) && regionName(in)
		}
		for _, line := range perlSplitLines(body) {
			if !hasNonSpace(line) {
				continue
			}
			if !r.ban && skipFlag(line) {
				continue
			}
			if !r.ban && rlit && flagIs(line, "skip_region_validation", "true") {
				continue
			}
			if !regionOrAlias(line) {
				r.hit("G3", f, bt, at)
				break
			}
		}
		x = findWord(bt, next, "provider", header)
	}
}

// regionLine is ^[ \t]*region[ \t]*=[ \t]*(.*?)[ \t]*$ on one line: the
// value's offsets.
func regionLine(l string) (int, int, bool) {
	i := skipBlank(l, 0)
	if !hasAt(l, i, "region") {
		return 0, 0, false
	}
	i = skipBlank(l, i+len("region"))
	if !hasAt(l, i, "=") {
		return 0, 0, false
	}
	v0 := skipBlank(l, i+1)
	v1 := len(l)
	for v1 > v0 && isBlank(l[v1-1]) {
		v1--
	}
	return v0, v1, true
}

// flagIs is \A\s*<name>\s*=\s*<value>\s*\z.
func flagIs(l, name, value string) bool {
	i := skipSpace(l, 0)
	if !hasAt(l, i, name) {
		return false
	}
	i = skipSpace(l, i+len(name))
	if !hasAt(l, i, "=") {
		return false
	}
	i = skipSpace(l, i+1)
	return hasAt(l, i, value) && skipSpace(l, i+len(value)) == len(l)
}

// skipFlag is $SKIPOK: a comment line, or a skip flag the laptop rules allow.
func skipFlag(l string) bool {
	i := skipSpace(l, 0)
	if hasAt(l, i, "#") || hasAt(l, i, "//") {
		return true
	}
	for _, n := range []string{"skip_metadata_api_check", "skip_credentials_validation", "skip_requesting_account_id"} {
		if flagIs(l, n, "true") || flagIs(l, n, "false") {
			return true
		}
	}
	return flagIs(l, "skip_region_validation", "false")
}

// regionOrAlias is \A\s*(?:region|alias)\s*=.
func regionOrAlias(l string) bool {
	i := skipSpace(l, 0)
	for _, w := range []string{"region", "alias"} {
		if hasAt(l, i, w) {
			return hasAt(l, skipSpace(l, i+len(w)), "=")
		}
	}
	return false
}

// sourceValue is the first match of /\bsource\s*=\s*"([^"]*)"/ in s.
func sourceValue(s string) (string, bool) {
	for x := findWord(s, 0, "source", atSourceAttr); x.start >= 0; x = findWord(s, x.start+1, "source", atSourceAttr) {
		if v, ok := sourceAfter(s, x.end); ok {
			return v, true
		}
	}
	return "", false
}

// laptopClone is the clone-only part: required_providers and module
// sources, read from the text with strings blanked.
func (r *run) laptopClone(f, raw string) {
	dir := dirOf(f)
	bl := blank(raw)
	blt := newText(bl)
	head := atWordSpace("required_providers")
	for x := findWord(bl, 0, "required_providers", head); x.start >= 0; {
		at, next := x.start, x.end
		e := blt.block(x.end)
		if e < 0 {
			r.hit("G1", f, raw, at)
			x = findWord(bl, next, "required_providers", head)
			continue
		}
		next = e
		b0, b1 := x.end+1, e-1
		bb, rb := bl[b0:b1], raw[b0:b1]
		entry := atProviderEntry(newText(bb))
		for y := findSpaceLines(bb, 0, entry); y.start >= 0; y = findSpaceLines(bb, y.end, entry) {
			n := bb[y.start:identEnd(bb, y.start)]
			v := rb[y.g0:y.g1]
			src := "hashicorp/" + n
			if strings.HasPrefix(v, "{") {
				if s, ok := sourceValue(v); ok {
					src = s
				}
			}
			if !laptopProviders[canon(src)] {
				r.hit("G1", f, raw, at)
			}
		}
		x = findWord(bl, next, "required_providers", head)
	}
	header := atHeader("module")
	sr := newSrcReads()
	for x := findWord(bl, 0, "module", header); x.start >= 0; {
		at, next := x.start, x.end
		e := blt.block(x.end)
		if e < 0 {
			r.hit("G2", f, raw, at)
			sr.refused[at] = true
			x = findWord(bl, next, "module", header)
			continue
		}
		next = e
		s, ok := sourceValue(raw[x.end:e])
		if ok && r.sourceOK(dir, s) {
			sr.pass[at] = s
		} else {
			r.hit("G2", f, raw, at)
			sr.refused[at] = true
		}
		x = findWord(bl, next, "module", header)
	}
	r.structuralSources(f, raw, sr)
}
