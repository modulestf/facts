package gates

import (
	"fmt"
	"slices"
	"strings"
)

// runnerBase are the providers every runner run allows, after the public
// registry's host.
var runnerBase = []string{"aws", "random", "time", "tls", "cloudinit", "null", "local", "external"}

// provSet is the runner's provider allowlist: the base, plus each extra
// ns/name, and each ns/* as any name of that namespace.
type provSet struct {
	exact map[string]bool
	ns    map[string]bool
}

// newProvSet checks each extra entry against ns/name or ns/*, the grammar
// of the profile's provider lines.
func newProvSet(extra []string) (provSet, error) {
	p := provSet{exact: map[string]bool{}, ns: map[string]bool{}}
	for _, n := range runnerBase {
		p.exact["hashicorp/"+n] = true
	}
	for _, e := range extra {
		ns, name, ok := strings.Cut(e, "/")
		if !ok || !allIn(ns, "-") || (name != "*" && !allIn(name, "-")) {
			return provSet{}, fmt.Errorf("provider %q is not ns/name or ns/*", e)
		}
		if name == "*" {
			p.ns[ns] = true
		} else {
			p.exact[e] = true
		}
	}
	return p, nil
}

// CheckProviders checks each extra runner provider against ns/name or ns/*.
func CheckProviders(extra []string) error {
	_, err := newProvSet(extra)
	return err
}

// ok reports whether c, a canon source, is allowed.
func (p provSet) ok(c string) bool {
	rest, found := strings.CutPrefix(c, "registry.terraform.io/")
	if !found {
		return false
	}
	if p.exact[rest] {
		return true
	}
	ns, name, found := strings.Cut(rest, "/")
	return found && p.ns[ns] && allIn(name, "-")
}

// credWords are $CRED: provider arguments that set credentials, a proxy or
// an endpoint.
var credWords = []string{"access_key", "secret_key", "token", "profile", "shared_credentials_file",
	"shared_credentials_files", "shared_config_files", "assume_role", "assume_role_with_web_identity",
	"role_arn", "endpoints", "custom_ca_bundle", "http_proxy", "https_proxy", "no_proxy", "insecure",
	"ec2_metadata_service_endpoint", "ec2_metadata_service_endpoint_mode"}

// argStart is (?:\A|[\s{;,]) before an argument name at q.
func argStart(s string, q int) bool {
	if q == 0 {
		return true
	}
	c := s[q-1]
	return isSpace(c) || c == '{' || c == ';' || c == ','
}

// hasCred is $rest =~ /(?:\A|[\s{;,])(?:$CRED)\s*[={]/.
func hasCred(s string) bool {
	for q := 0; q < len(s); q++ {
		if !argStart(s, q) {
			continue
		}
		for _, w := range credWords {
			if hasAt(s, q, w) {
				if k := skipSpace(s, q+len(w)); k < len(s) && (s[k] == '=' || s[k] == '{') {
					return true
				}
			}
		}
	}
	return false
}

// credLabel is /\A\s*(?:$CRED)\s*\z/.
func credLabel(s string) bool {
	i, j := 0, len(s)
	for i < j && isSpace(s[i]) {
		i++
	}
	for j > i && isSpace(s[j-1]) {
		j--
	}
	return slices.Contains(credWords, s[i:j])
}

// findAssumeRole is /(?:\A|[\s{;,])assume_role\s*($B)/ in s: the match
// start and the block.
func findAssumeRole(s string) (s0, i0, i1 int, ok bool) {
	t := newText(s)
	try := func(q int) (int, int, bool) {
		if !hasAt(s, q, "assume_role") {
			return 0, 0, false
		}
		b := skipSpace(s, q+len("assume_role"))
		if e := t.block(b); e >= 0 {
			return b, e, true
		}
		return 0, 0, false
	}
	for s0 := 0; s0 < len(s); s0++ {
		if s0 == 0 {
			if b, e, ok := try(0); ok {
				return 0, b, e, true
			}
		}
		if c := s[s0]; isSpace(c) || c == '{' || c == ';' || c == ',' {
			if b, e, ok := try(s0 + 1); ok {
				return s0, b, e, true
			}
		}
	}
	return 0, 0, 0, false
}

// roleLine is ^[ \t]*(?:role_arn|session_name)[ \t]*=[ \t]*"[ ]*"[ \t]*$.
func roleLine(l string) bool {
	i := skipBlank(l, 0)
	switch {
	case hasAt(l, i, "role_arn"):
		i += len("role_arn")
	case hasAt(l, i, "session_name"):
		i += len("session_name")
	default:
		return false
	}
	i = skipBlank(l, i)
	if !hasAt(l, i, "=") {
		return false
	}
	i = skipBlank(l, i+1)
	if !hasAt(l, i, `"`) {
		return false
	}
	i++
	for i < len(l) && l[i] == ' ' {
		i++
	}
	if !hasAt(l, i, `"`) {
		return false
	}
	return skipBlank(l, i+1) == len(l)
}

// roleARN is \Aarn:aws[a-z-]*:iam::[0-9]{12}:role/[\w+=,.\@/-]+\z.
func roleARN(v string) bool {
	rest, ok := strings.CutPrefix(v, "arn:aws")
	if !ok {
		return false
	}
	i := 0
	for i < len(rest) && (rest[i] >= 'a' && rest[i] <= 'z' || rest[i] == '-') {
		i++
	}
	rest, ok = strings.CutPrefix(rest[i:], ":iam::")
	if !ok || len(rest) < 12 || !allDigits(rest[:12]) {
		return false
	}
	name, ok := strings.CutPrefix(rest[12:], ":role/")
	if !ok || name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; !isWord(c) && !strings.ContainsRune("+=,.@/-", rune(c)) {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// fixedRole is the Perl's fixed_role: an assume_role block of one literal
// role ARN and an optional literal session name, nothing else. m and r are
// its masked and raw inner text.
func fixedRole(m, r string) bool {
	lines := strings.Split(m, "\n")
	for _, l := range lines {
		if !roleLine(l) && hasNonSpace(l) {
			return false
		}
	}
	n, off := 0, 0
	for _, l := range lines {
		i := skipBlank(l, 0)
		if hasAt(l, i, "role_arn") {
			j := skipBlank(l, i+len("role_arn"))
			if hasAt(l, j, "=") {
				if k := skipBlank(l, j+1); hasAt(l, k, `"`) {
					v, _ := sourceAfter(r, off+k+1)
					if !roleARN(v) {
						return false
					}
					n++
				}
			}
		}
		off += len(l) + 1
	}
	return n == 1
}

func hasNonSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isSpace(s[i]) {
			return true
		}
	}
	return false
}

// buildInDocker is ^[ \t]*build_in_docker[ \t]*=[ \t]*true[ \t]*$ in some
// line of s.
func buildInDocker(s string) bool {
	for l := range strings.SplitSeq(s, "\n") {
		i := skipBlank(l, 0)
		if !hasAt(l, i, "build_in_docker") {
			continue
		}
		i = skipBlank(l, i+len("build_in_docker"))
		if !hasAt(l, i, "=") {
			continue
		}
		i = skipBlank(l, i+1)
		if hasAt(l, i, "true") && skipBlank(l, i+4) == len(l) {
			return true
		}
	}
	return false
}

// runnerFile is the Perl's runner_file: the gates of one file in runner
// mode, over its mask.
func (r *run) runnerFile(f, raw string) {
	m, ok := mask(raw)
	if !ok {
		r.hitFile("G0", f)
		return
	}
	mt := newText(m)
	if i := findADJ(m); i >= 0 {
		r.hit("G3", f, m, i)
	}
	if i := firstNonASCII(m); i >= 0 {
		r.hit("G0", f, m, i)
	}
	for x := findLines(m, 0, atLabelLine); x.start >= 0; x = findLines(m, x.end, atLabelLine) {
		// The Perl tests the raw label with a second match, which resets the
		// offsets its hit then reads, so it prints line 0 here; the laptop
		// rule kept its offset and prints the line.
		if firstNonASCII(raw[x.g0:x.g1]) >= 0 {
			r.hitFile("G0", f)
		}
	}
	for x := findFrom(m, 0, atKW); x.start >= 0; x = findFrom(m, x.end, atKW) {
		r.hit("G3", f, m, x.start)
	}
	for _, t := range []string{raw, m} {
		for x := findFrom(t, 0, atCloudBackend); x.start >= 0; x = findFrom(t, x.end, atCloudBackend) {
			r.hit("G3", f, t, x.start)
		}
	}
	r.runnerProviders(f, raw, m, mt)
	r.runnerRequired(f, raw, m, mt)
	header := atHeader("module")
	for x := findWord(m, 0, "module", header); x.start >= 0; {
		next := x.end
		if e := mt.block(x.end); e >= 0 {
			next = e
			if buildInDocker(fillBlocks(m[x.end+1 : e-1])) {
				r.hit("Xdocker", f, m, x.start)
			}
		}
		x = findWord(m, next, "module", header)
	}
	if r.o.Downloaded {
		return
	}
	dir := dirOf(f)
	sr := newSrcReads()
	for x := findWord(m, 0, "module", header); x.start >= 0; {
		next := x.end
		e := mt.block(x.end)
		pass, s := false, ""
		if e >= 0 {
			next = e
			if z := findWord(m[x.end:e], 0, "source", atSourceAttr); z.start >= 0 {
				s, _ = sourceAfter(raw, x.end+z.end)
				pass = r.sourceOK(dir, s)
			}
		}
		if pass {
			sr.pass[x.start] = s
		} else {
			r.hit("G2", f, m, x.start)
			sr.refused[x.start] = true
		}
		x = findWord(m, next, "module", header)
	}
	r.structuralSources(f, raw, sr)
}

// runnerProviders checks each aws provider block for credentials and
// records a fixed role as an exception.
func (r *run) runnerProviders(f, raw, m string, mt *text) {
	header := atHeader("provider")
	dynamic := atHeader("dynamic")
	for x := findWord(m, 0, "provider", header); x.start >= 0; {
		at, next := x.start, x.end
		name := ""
		if q, ok := firstQuoted(raw[at:x.end]); ok {
			name = asciiLower(q)
		}
		e := mt.block(x.end)
		if e < 0 {
			r.hit("G3", f, m, at)
			x = findWord(m, next, "provider", header)
			continue
		}
		next = e
		if name == "aws" || name == "awscc" {
			b0 := x.end + 1
			body := m[b0 : e-1]
			rest, exc := body, false
			if s0, i0, i1, ok := findAssumeRole(body); ok && fixedRole(body[i0+1:i1-1], raw[b0+i0+1:b0+i1-1]) {
				exc = true
				rest = rest[:s0] + fill(rest[s0:i1]) + rest[i1:]
			}
			bad := strings.Contains(rest, "<<") || hasCred(rest)
			for d := findWord(rest, 0, "dynamic", dynamic); !bad && d.start >= 0; d = findWord(rest, d.end, "dynamic", dynamic) {
				bad = credLabel(raw[b0+d.g0+1 : b0+d.g1-1])
			}
			if bad {
				r.hit("G3", f, m, at)
			} else if exc {
				r.hit("Xrole", f, m, at)
			}
		}
		x = findWord(m, next, "provider", header)
	}
}

// runnerRequired checks each required_providers entry: the Docker provider
// is an exception, another source outside the allowlist a G1 hit, and so is
// a local name that is not its source's type.
func (r *run) runnerRequired(f, raw, m string, mt *text) {
	head := atWordSpace("required_providers")
	for x := findWord(m, 0, "required_providers", head); x.start >= 0; {
		at, next := x.start, x.end
		e := mt.block(x.end)
		if e < 0 {
			r.hit("G1", f, m, at)
			x = findWord(m, next, "required_providers", head)
			continue
		}
		next = e
		b0 := x.end + 1
		bb := m[b0 : e-1]
		entry := atProviderEntry(newText(bb))
		for y := findSpaceLines(bb, 0, entry); y.start >= 0; y = findSpaceLines(bb, y.end, entry) {
			n := bb[y.start:identEnd(bb, y.start)]
			src := "hashicorp/" + n
			if z := findWord(bb[y.g0:y.g1], 0, "source", atSourceAttr); z.start >= 0 {
				src, _ = sourceAfter(raw, b0+y.g0+z.end)
			}
			c := canon(src)
			if c == "registry.terraform.io/kreuzwerker/docker" {
				r.hit("Xdocker", f, m, at)
			} else if !r.o.Downloaded && !r.prov.ok(c) {
				r.hit("G1", f, m, at)
			}
			if asciiLower(n) != lastPart(c) {
				r.hit("G1", f, m, at)
			}
		}
		x = findWord(m, next, "required_providers", head)
	}
}
