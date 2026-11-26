package facts

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// This file reads what pre-commit-terraform's hooks write and pin: the
// wrapper versions.tf the terraform_wrapper_module_for_each hook derives from
// a module's versions.tf, the hook's pin in .pre-commit-config.yaml, and the
// terraform-docs hook and workflow pin. Every reader is a line reader that
// fails closed: input it might read another way than pre-commit, a YAML
// loader or the hook gives "unknown" or "custom", never a match. A positive
// result of the YAML readers is confirmed by readYAML, the structural reader
// the scope rules use.

// MaxWrapperFileBytes bounds a versions.tf derive reads and the
// .pre-commit-config.yaml WrapperPin reads.
const MaxWrapperFileBytes = 65536

// MaxDocsFileBytes bounds the .pre-commit-config.yaml DocsHook reads and the
// workflow DocsWorkflowPin reads.
const MaxDocsFileBytes = 1 << 20

var (
	deriveBlock   = regexp.MustCompile(`^([ \t]*)provider_meta "[^"]*" \{$`)
	deriveSkip    = regexp.MustCompile(`^[ \t]*(#|//|$)`)
	deriveToken   = regexp.MustCompile(`^[ \t]*([A-Za-z0-9_-]+)`)
	deriveList    = regexp.MustCompile(`^[ \t]*(user_agent|module_name) *= *\[[ \t]*$`)
	deriveListEnd = regexp.MustCompile(`^[ \t]*\]`)
	deriveAttr    = regexp.MustCompile(`^[ \t]*[A-Za-z_][A-Za-z0-9_-]* *= *(\S.*)$`)
)

// WrapperDerive returns the wrapper versions.tf the hook writes, from
// v1.106.0 on, for a module's versions.tf: the module file with user_agent
// and module_name removed from each provider_meta block. ok is false when the
// result is undefined: over MaxWrapperFileBytes, no final newline, a byte
// outside printable ASCII, tab and newline, a heredoc marker anywhere, or a
// provider_meta block holding anything but comments, blank lines and
// single-line attributes, with the two removed attributes also allowed as a
// bracketed list over several lines.
func WrapperDerive(s []byte) ([]byte, bool) {
	if len(s) > MaxWrapperFileBytes || !bytes.HasSuffix(s, []byte("\n")) || bytes.Contains(s, []byte("<<")) {
		return nil, false
	}
	for _, c := range s {
		if (c < 0x20 || c > 0x7e) && c != '\t' && c != '\n' {
			return nil, false
		}
	}
	l := strings.Split(string(s[:len(s)-1]), "\n")
	var o strings.Builder
	for i := 0; i < len(l); {
		x := l[i]
		i++
		o.WriteString(x + "\n")
		m := deriveBlock.FindStringSubmatch(x)
		if m == nil {
			continue
		}
		end, closed := m[1]+"}", false
		for i < len(l) {
			y := l[i]
			i++
			if y == end {
				o.WriteString(y + "\n")
				closed = true
				break
			}
			if strings.ContainsAny(y, "{}") {
				return nil, false
			}
			if deriveSkip.MatchString(y) {
				o.WriteString(y + "\n")
				continue
			}
			t := deriveToken.FindStringSubmatch(y)
			ua := t != nil && (t[1] == "user_agent" || t[1] == "module_name")
			if ua && deriveList.MatchString(y) {
				for {
					if i >= len(l) {
						return nil, false
					}
					z := l[i]
					i++
					if strings.ContainsAny(z, "{}") {
						return nil, false
					}
					if deriveListEnd.MatchString(z) {
						break
					}
				}
				continue
			}
			v := deriveAttr.FindStringSubmatch(y)
			if v == nil || (strings.Contains(v[1], "[") && (!ua || !bracketsClose(v[1]))) {
				return nil, false
			}
			if !ua {
				o.WriteString(y + "\n")
			}
		}
		if !closed {
			return nil, false
		}
	}
	return []byte(o.String()), true
}

// bracketsClose reports whether every [ in s is closed and no ] opens early.
func bracketsClose(s string) bool {
	d := 0
	for _, c := range s {
		switch c {
		case '[':
			d++
		case ']':
			if d--; d < 0 {
				return false
			}
		}
	}
	return d == 0
}

// Results of WrapperPin that are no mode.
const (
	PinUnreadable   = "unknown pin unreadable"
	PinNone         = "unknown no pin"
	pinHookOptions  = "unknown hook options"
	pinHookUnlisted = "unknown hook not listed"
	pinNotTag       = "unknown pin not a release tag"
	pinOldHook      = "unknown old hook"
	pinDerive       = "derive"
	pinCopy         = "copy"
	wrapperHookID   = "terraform_wrapper_module_for_each"
	docsHookID      = "terraform_docs"
	docsProfileArgs = "--args=--lockfile=false"
)

var (
	pinReposKey   = regexp.MustCompile(`(?mi)^["']?repos["']?[ \t]*:`)
	pinOptions    = regexp.MustCompile(`(?i)module-repo-provider`)
	pinScalarOpen = regexp.MustCompile(`(^|[ \t:-])[|>][-+0-9]*[ \t]*$`)
	pinAnchor     = regexp.MustCompile(`(?m)(^|[\s\[{,])[&*!]\S`)
	pinComplexKey = regexp.MustCompile(`(?m)^[ \t]*(-[ \t]+)?(\?|<<)`)
	pinURL        = regexp.MustCompile(`^https://github\.com/antonbabenko/pre-commit-terraform(?:\.git)?$`)
	pinLineForm   = []*regexp.Regexp{
		regexp.MustCompile(`^[ \t]*(#|$)`),
		regexp.MustCompile(`^[ \t]*-([ \t]|$)`),
		regexp.MustCompile(`^[ \t]*["']?[A-Za-z_][A-Za-z0-9_.-]*["']?:([ \t]|$)`),
	}
	pinRepoRev   = regexp.MustCompile(`(?i)^[ \t]*(-[ \t]+)?["']?(repo|rev)["']?[ \t]*:`)
	pinRepoForm  = regexp.MustCompile(`^(https://[A-Za-z0-9._/-]+|local|meta)$`)
	pinItemStart = regexp.MustCompile(`^([ \t]*)-([ \t]+)repo`)
	pinName      = regexp.MustCompile(`(?i)pre-commit-terraform`)
	pinHookName  = regexp.MustCompile(`(?i)terraform_wrapper_module_for_each`)
	pinBareVer   = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	pinTag       = regexp.MustCompile(`^v([0-9]{1,5})\.([0-9]{1,5})\.([0-9]{1,5})$`)
	// Each quoted form below is tried with a double quote, a single quote and
	// no quote, as a backreference to an optional quote would be.
	pinRepoLine = quoteForms(`^[ \t]*-[ \t]+repo:[ \t]+Q(\S+?)Q([ \t]+#.*)?[ \t]*$`)
	pinRevLine  = quoteForms(`^[ \t]+rev:[ \t]+Q([^"'\s#]+)Q([ \t]+#.*)?[ \t]*$`)
	pinHookLine = quoteForms(`^[ \t]*-[ \t]+id:[ \t]+Q` + wrapperHookID + `Q[ \t]*(#.*)?$`)
)

// quoteForms compiles pattern with each Q replaced by a double quote, a
// single quote and nothing, in that order.
func quoteForms(pattern string) [3]*regexp.Regexp {
	var out [3]*regexp.Regexp
	for i, q := range []string{`"`, `'`, ``} {
		out[i] = regexp.MustCompile(strings.ReplaceAll(pattern, "Q", q))
	}
	return out
}

// matchQuoted matches s against the quote forms. A form whose opening quote
// is not the character it would consume is skipped, so the result is that
// of a pattern with an optional quote and a backreference to it.
func matchQuoted(forms [3]*regexp.Regexp, s string) []string {
	for _, re := range forms {
		if m := re.FindStringSubmatch(s); m != nil {
			return m
		}
	}
	return nil
}

// pinEntry is one entry of the hook's repository in the line read.
type pinEntry struct {
	revs   []string
	hook   bool
	indent int
}

// WrapperPin reads the pre-commit-terraform pin of a .pre-commit-config.yaml
// as data: "derive" for a release tag at v1.106.0 or later, "copy" for one
// from v1.89.0, or "unknown <reason>". The caller gives PinNone for an absent
// file and PinUnreadable for one it cannot read.
func WrapperPin(data []byte) string {
	s := string(data)
	r, rev := wrapperPinLines(s)
	if (r == pinDerive || r == pinCopy) && !pinConfirmed(data, rev) {
		return PinUnreadable
	}
	return r
}

// wrapperPinLines is the line reader of WrapperPin. rev is the entry's one
// rev when the result is a mode.
func wrapperPinLines(s string) (result, rev string) {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 0x20 || c > 0x7e) && c != '\t' && c != '\n' {
			return PinUnreadable, ""
		}
	}
	// An escape, or anything but one repos key at column 0.
	if strings.Contains(s, `\`) || len(pinReposKey.FindAllStringIndex(s, -1)) != 1 {
		return PinUnreadable, ""
	}
	if pinOptions.MatchString(s) {
		return pinHookOptions, ""
	}
	lines := strings.Split(s, "\n")
	for _, l := range lines {
		if !pinLineClosed(l) {
			return PinUnreadable, ""
		}
	}
	if pinAnchor.MatchString(s) || pinComplexKey.MatchString(s) {
		return PinUnreadable, ""
	}
	in, e := false, []pinEntry(nil)
	for _, l := range lines {
		if !matchesAny(pinLineForm, l) {
			return PinUnreadable, ""
		}
		repo, rev := matchQuoted(pinRepoLine, l), matchQuoted(pinRevLine, l)
		if pinRepoRev.MatchString(l) && repo == nil && rev == nil {
			return PinUnreadable, ""
		}
		if repo != nil {
			if !pinRepoForm.MatchString(repo[1]) {
				return PinUnreadable, ""
			}
			in = pinURL.MatchString(repo[1])
			if m := pinItemStart.FindStringSubmatch(l); in && m != nil {
				e = append(e, pinEntry{indent: len(m[1]) + 1 + len(m[2])})
			}
			continue
		}
		if l != "" && l[0] != ' ' && l[0] != '\t' {
			in = false
			continue
		}
		if !in {
			continue
		}
		last := &e[len(e)-1]
		if rev != nil {
			if len(l)-len(strings.TrimLeft(l, " \t")) != last.indent {
				return PinUnreadable, ""
			}
			last.revs = append(last.revs, rev[1])
		}
		if matchQuoted(pinHookLine, l) != nil {
			last.hook = true
		}
	}
	if len(pinHookName.FindAllStringIndex(s, -1)) > 1 {
		return PinUnreadable, ""
	}
	m := len(pinName.FindAllStringIndex(s, -1))
	if m == 0 {
		return PinNone, ""
	}
	if len(e) != 1 || m != 1 || len(e[0].revs) != 1 {
		return PinUnreadable, ""
	}
	if !e[0].hook {
		return pinHookUnlisted, ""
	}
	rev = e[0].revs[0]
	if pinBareVer.MatchString(rev) {
		return PinUnreadable, ""
	}
	v := pinTag.FindStringSubmatch(rev)
	if v == nil {
		return pinNotTag, ""
	}
	// The hook copies the module file from v1.89.0 and strips the user agent
	// from v1.106.0; before v1.89.0 it wrote a fixed template.
	k := fmt.Sprintf("%05s%05s%05s", v[1], v[2], v[3])
	switch {
	case k >= "000010010600000":
		return pinDerive, rev
	case k >= "000010008900000":
		return pinCopy, rev
	}
	return pinOldHook, ""
}

// pinLineClosed reports, for one line outside quoted text and comments, that
// no quote is left open, no flow mapping opens, a flow sequence closes on the
// line and no block scalar starts.
func pinLineClosed(l string) bool {
	var b strings.Builder
	q, d := byte(0), 0
	for i := 0; i < len(l); i++ {
		c := l[i]
		if q != 0 {
			if c == q {
				q = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			q = c
			b.WriteByte('Q')
			continue
		}
		if c == '#' && (b.Len() == 0 || strings.HasSuffix(b.String(), " ") || strings.HasSuffix(b.String(), "\t")) {
			break
		}
		b.WriteByte(c)
		switch c {
		case '[':
			d++
		case '{':
			return false
		case ']':
			if d--; d < 0 {
				return false
			}
		}
	}
	return q == 0 && d == 0 && !pinScalarOpen.MatchString(b.String())
}

func matchesAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// pinConfirmed reads the configuration with readYAML and confirms the line
// reader's result: one repos list, exactly one entry of the hook's
// repository, whose rev is rev and whose hooks list the wrapper hook. Any
// other structure, or YAML outside readYAML's subset, is no confirmation.
func pinConfirmed(data []byte, rev string) bool {
	ls, hooks, ok := hookRepo(data)
	if !ok {
		return false
	}
	rf, _ := fields(ls, hooks.entry)
	ri, has := rf["rev"]
	if !has || ls[ri].val != rev {
		return false
	}
	for _, h := range hooks.ids {
		if ls[h].val == wrapperHookID {
			return true
		}
	}
	return false
}

// hookEntry is the entry of the hook's repository in readYAML's lines: the
// item line, and the id line of each hook it lists.
type hookEntry struct {
	entry int
	ids   []int
	items []int
}

// hookRepo finds the one entry of pre-commit-terraform under the top-level
// repos list. ok is false for YAML outside readYAML's subset, a repos key
// that holds no block list, or other than exactly one such entry.
func hookRepo(data []byte) ([]yamlLine, hookEntry, bool) {
	ls, ok := readYAML(data)
	if !ok {
		return nil, hookEntry{}, false
	}
	var found []hookEntry
	for i, l := range ls {
		if l.indent != 0 || l.item || l.key != "repos" {
			continue
		}
		repos, ok := listItems(ls, i)
		if !ok {
			return nil, hookEntry{}, false
		}
		for _, r := range repos {
			rf, ok := fields(ls, r)
			if !ok {
				return nil, hookEntry{}, false
			}
			ri, has := rf["repo"]
			if !has || !pinURL.MatchString(ls[ri].val) {
				continue
			}
			he := hookEntry{entry: r}
			if h, has := rf["hooks"]; has {
				items, ok := listItems(ls, h)
				if !ok {
					return nil, hookEntry{}, false
				}
				for _, k := range items {
					hf, ok := fields(ls, k)
					if !ok {
						return nil, hookEntry{}, false
					}
					if j, has := hf["id"]; has {
						he.ids = append(he.ids, j)
						he.items = append(he.items, k)
					}
				}
			}
			found = append(found, he)
		}
	}
	if len(found) != 1 {
		return nil, hookEntry{}, false
	}
	return ls, found[0], true
}

// Results of DocsHook.
const (
	DocsHookProfile = "profile"
	DocsHookCustom  = "custom"
)

var (
	docsAnchor     = regexp.MustCompile(`(?m)(^|[\s,])[&*!]\S`)
	docsComplexKey = regexp.MustCompile(`(?m)^[ ]*(-[ ]+)?(\?|<<)`)
	docsScalar     = regexp.MustCompile(`(?m)(^|[ :-])[|>][-+0-9]*[ ]*(#.*)?$`)
	docsHookLine   = quoteForms(`^( *)- +id: +Q` + docsHookID + `Q *(#.*)?$`)
	docsRepoLine   = quoteForms(`^ *- +repo: +Q(\S+?)Q *(#.*)?$`)
	docsArgLine    = quoteForms(`^ *- +Q([^"'#]*?)Q *(#.*)?$`)
	docsBlank      = regexp.MustCompile(`^ *(#.*)?$`)
	docsArgsKey    = regexp.MustCompile(`^ *args: *(#.*)?$`)
	docsURL        = regexp.MustCompile(`^https://github\.com/antonbabenko/pre-commit-terraform(\.git)?$`)
)

// DocsHook reads the terraform_docs hook of a .pre-commit-config.yaml as
// data: "profile" when it is the one hook of pre-commit-terraform with that
// id and its only setting is the args list holding the single
// --args=--lockfile=false, else "custom". The caller gives custom for an
// absent or unreadable file.
func DocsHook(data []byte) string {
	if docsHookLines(string(data)) && docsConfirmed(data) {
		return DocsHookProfile
	}
	return DocsHookCustom
}

// docsHookLines is the line reader of DocsHook.
func docsHookLines(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 0x20 || c > 0x7e) && c != '\n' {
			return false
		}
	}
	if strings.ContainsAny(s, `\{}[]`) || docsAnchor.MatchString(s) || docsComplexKey.MatchString(s) || docsScalar.MatchString(s) {
		return false
	}
	l := strings.Split(s, "\n")
	for len(l) > 0 && l[len(l)-1] == "" {
		l = l[:len(l)-1]
	}
	i := -1
	for j, x := range l {
		if strings.Contains(x, docsHookID) {
			if i >= 0 {
				return false
			}
			i = j
		}
	}
	if i < 0 {
		return false
	}
	m := matchQuoted(docsHookLine, l[i])
	if m == nil {
		return false
	}
	ind := len(m[1])
	repo := ""
	for j := i - 1; j >= 0; j-- {
		if r := matchQuoted(docsRepoLine, l[j]); r != nil {
			repo = r[1]
			break
		}
	}
	if !docsURL.MatchString(repo) {
		return false
	}
	var args []string
	inArgs := false
	for _, t := range l[i+1:] {
		if docsBlank.MatchString(t) {
			continue
		}
		n := len(t) - len(strings.TrimLeft(t, " "))
		if n <= ind {
			break
		}
		if n == ind+2 && docsArgsKey.MatchString(t) {
			inArgs = true
			continue
		}
		if a := matchQuoted(docsArgLine, t); inArgs && n > ind+2 && a != nil {
			args = append(args, a[1])
			continue
		}
		return false
	}
	return len(args) == 1 && args[0] == docsProfileArgs
}

// docsConfirmed confirms DocsHook's line read with readYAML: in the one entry
// of pre-commit-terraform, the terraform_docs hook has id and args alone, and
// args is a block list of the profile's one argument.
func docsConfirmed(data []byte) bool {
	ls, he, ok := hookRepo(data)
	if !ok {
		return false
	}
	for n, j := range he.ids {
		if ls[j].val != docsHookID {
			continue
		}
		hf, _ := fields(ls, he.items[n])
		a, has := hf["args"]
		if !has || len(hf) != 2 {
			return false
		}
		items, ok := listItems(ls, a)
		return ok && len(items) == 1 && ls[items[0]].key == "" && ls[items[0]].val == docsProfileArgs
	}
	return false
}

// Results of DocsWorkflowPin.
const (
	WorkflowSame       = "same"
	WorkflowMismatch   = "mismatch"
	WorkflowNone       = "none"
	WorkflowUnreadable = "unreadable"
)

var (
	wfKey     = regexp.MustCompile(`^[ \t]*(TERRAFORM_DOCS_VERSION|terraform-docs-version)[ \t]*:`)
	wfPrefix  = regexp.MustCompile(`^[^:]*:[ \t]*`)
	wfComment = regexp.MustCompile(`[ \t]+#.*$`)
	wfTrail   = regexp.MustCompile(`[ \t]+$`)
	wfEnvRef  = regexp.MustCompile(`^\$\{\{ *env\.TERRAFORM_DOCS_VERSION *\}\}$`)
	wfVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)
)

// DocsWorkflowPin reads the terraform-docs version a workflow pins, in a
// TERRAFORM_DOCS_VERSION or terraform-docs-version key, against version:
// "same" when every pin equals it, "mismatch" when one differs, "none" when
// there is no pin and "unreadable" when a pin is not a plain version. A
// reference to the env key is no pin of its own. The caller gives none for
// an absent file and unreadable for one it cannot read.
func DocsWorkflowPin(data []byte, version string) string {
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	bad, mis, n := false, false, 0
	for _, l := range lines {
		if !wfKey.MatchString(l) {
			continue
		}
		v := wfPrefix.ReplaceAllString(l, "")
		v = replaceFirst(wfComment, v)
		v = replaceFirst(wfTrail, v)
		if wfEnvRef.MatchString(v) {
			continue
		}
		// One quote off each end, as gsub(/^["']|["']$/, "") does.
		if strings.HasPrefix(v, `"`) || strings.HasPrefix(v, "'") {
			v = v[1:]
		}
		if strings.HasSuffix(v, `"`) || strings.HasSuffix(v, "'") {
			v = v[:len(v)-1]
		}
		if !wfVersion.MatchString(v) {
			bad = true
			continue
		}
		if !strings.HasPrefix(v, "v") {
			v = "v" + v
		}
		n++
		if v != version {
			mis = true
		}
	}
	switch {
	case bad:
		return WorkflowUnreadable
	case mis:
		return WorkflowMismatch
	case n > 0:
		return WorkflowSame
	}
	return WorkflowNone
}

// replaceFirst removes the leftmost match of re in s, as awk's sub does.
func replaceFirst(re *regexp.Regexp, s string) string {
	if loc := re.FindStringIndex(s); loc != nil {
		return s[:loc[0]] + s[loc[1]:]
	}
	return s
}

// Results of WrapperResult beside the pin's own unknown results.
const (
	WrapperDerived         = "derived"
	WrapperCopied          = "copied"
	WrapperDiffers         = "differs"
	wrapperProviderMeta    = "unknown provider_meta"
	wrapperDeriveUndefined = "unknown derive undefined"
)

var (
	metaLine = regexp.MustCompile(`^[ \t\v\f\r]*provider_meta`)
	metaAWS  = regexp.MustCompile(`^[ \t\v\f\r]*provider_meta "aws" \{$`)
)

// WrapperResult classifies a wrapper versions.tf against its module's under
// the pin's result: "derived" or "copied" when the wrapper is what the hook
// writes at that pin, "differs" when it is not, or "unknown <reason>". The
// hook strips only its own provider's block, aws by default, and derive
// strips every block, so a module with any other provider_meta line is no
// evidence.
func WrapperResult(pin string, module, wrapper []byte) string {
	switch pin {
	case pinDerive:
		if otherProviderMeta(module) {
			return wrapperProviderMeta
		}
		d, ok := WrapperDerive(module)
		if !ok {
			return wrapperDeriveUndefined
		}
		if bytes.Equal(d, wrapper) {
			return WrapperDerived
		}
		return WrapperDiffers
	case pinCopy:
		if bytes.Equal(module, wrapper) {
			return WrapperCopied
		}
		return WrapperDiffers
	}
	return pin
}

// otherProviderMeta reports a provider_meta line that is not the aws block's
// opening line. A file with a NUL byte that holds any provider_meta line
// counts, as grep reads such a file as binary.
func otherProviderMeta(s []byte) bool {
	l := strings.Split(string(s), "\n")
	nul := bytes.IndexByte(s, 0) >= 0
	for _, x := range l {
		if metaLine.MatchString(x) && (nul || !metaAWS.MatchString(x)) {
			return true
		}
	}
	return false
}
