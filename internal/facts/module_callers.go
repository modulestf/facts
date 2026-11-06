package facts

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/modulestf/facts/internal/profile"
)

// maxModuleCallers caps the caller records of module_callers across all
// modules. More is an error, so a report never holds a partial list.
const maxModuleCallers = 4096

// ErrCallerLimit is returned when the head tree has more than
// maxModuleCallers local calls of the modules the change touches.
var ErrCallerLimit = errors.New("module_caller_limit_exceeded")

// DowngradeInRepo is the downgrade marker of a candidate in an in-repo
// downgrade area.
const DowngradeInRepo = "in-repo"

// ModuleCallers is one module the change touches, with every local call of
// it in the head tree. Complete is one value for the whole tree: false when
// any call may have been missed. See BuildModuleCallers.
type ModuleCallers struct {
	Module   string         `json:"module"`
	Complete bool           `json:"complete"`
	Callers  []ModuleCaller `json:"callers"`
}

// ModuleCaller is one local module call: the area that owns the calling file,
// the file, the line of its source argument, and whether the change touches
// that file.
type ModuleCaller struct {
	Area        string `json:"area"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	FileChanged bool   `json:"file_changed"`
}

// remoteSource matches the module sources Terraform fetches from elsewhere,
// which never name a directory of this tree: a registry address with an
// optional host, and the go-getter forms with a forced getter or a known
// host.
var remoteSource = regexp.MustCompile(`^(` +
	`([A-Za-z0-9.-]+\.[A-Za-z]{2,}(:[0-9]+)?/)?[A-Za-z0-9_-]+/[A-Za-z0-9_-]+/[A-Za-z0-9_-]+(//.*)?` +
	`|(git|hg|s3|gcs)::.+` +
	`|https?://.+` +
	`|(github\.com|bitbucket\.org)/.+` +
	`)$`)

// callersScan is one scan of the head tree for local module calls.
type callersScan struct {
	head, base *Tree
	change     string
	targets    map[string]bool // area paths a call may name: root, submodule, module
	wanted     map[string]bool // the areas that get an entry
	mods       []string
	complete   bool
	byModule   map[string][]ModuleCaller
	records    int
}

// BuildModuleCallers lists, for every head area of kind root, submodule or
// module that the change touches, the local calls of it in every head .tf
// file, those under tests/, wrappers/ and examples/ included. With no change
// the list is empty. A local source starts ./ or ../ and must name exactly
// one area path once joined to the calling file's directory; anything else
// that is not a known remote form, a source that is not a literal, a
// Terraform file the engine could not parse, any link, special or unreadable
// entry, any test, Terragrunt, other .hcl or OpenTofu file (see
// unparsedConfig) and any change path AreaOf refuses make the scan
// incomplete. touched holds the areas the change touches.
func BuildModuleCallers(head, base *Tree, change string, areas []*Area, touched map[string]bool, lim Limits) ([]ModuleCallers, error) {
	out := []ModuleCallers{}
	if change == ChangeNone || base == nil {
		return out, nil
	}
	s := &callersScan{head: head, base: base, change: change, targets: map[string]bool{}, wanted: map[string]bool{},
		complete: true, byModule: map[string][]ModuleCaller{}}
	parsed := map[string]*Module{}
	for _, a := range areas {
		if a.Kind == AreaModule {
			s.mods = append(s.mods, a.Path)
		}
		if a.Kind == AreaExample {
			continue
		}
		s.targets[a.Path] = true
		if touched[a.Path] && a.HeadExists {
			s.wanted[a.Path] = true
		}
		if a.HeadExists && a.Issue == "" && a.Head != nil {
			parsed[a.Path] = a.Head
		}
	}
	slices.Sort(s.mods)
	if len(s.wanted) == 0 {
		return out, nil
	}
	s.checkEntries()
	if err := s.scanDirs(parsed, lim); err != nil {
		return nil, err
	}
	for _, m := range slices.Sorted(maps.Keys(s.wanted)) {
		cs := s.byModule[m]
		if cs == nil {
			cs = []ModuleCaller{}
		}
		slices.SortFunc(cs, func(x, y ModuleCaller) int {
			return cmp.Or(strings.Compare(x.Area, y.Area), strings.Compare(x.File, y.File), cmp.Compare(x.Line, y.Line))
		})
		out = append(out, ModuleCallers{Module: m, Complete: s.complete, Callers: cs})
	}
	return out, nil
}

// checkEntries clears complete for an opaque head entry, a test file, or a
// change path AreaOf refuses.
func (s *callersScan) checkEntries() {
	for _, p := range s.head.Paths {
		e := s.head.Entries[p]
		if e.Kind == KindSymlink || e.Kind == KindSpecial || e.Kind == KindUnreadable || unparsedConfig(p) {
			s.complete = false
		}
		if !sameEntry(e, s.base.Entries[p]) && !cleanRelative(p) {
			s.complete = false
		}
	}
	for _, p := range s.base.Paths {
		if s.head.Entries[p] == nil && !cleanRelative(p) {
			s.complete = false
		}
	}
}

// unparsedConfig reports a file that may call a module but that the engine
// does not parse: a test file, Terragrunt or other HCL configuration but the
// provider lock file, and OpenTofu configuration.
func unparsedConfig(p string) bool {
	name := path.Base(p)
	switch {
	case strings.HasSuffix(name, ".tftest.json"), strings.HasSuffix(name, ".tofu"), strings.HasSuffix(name, ".tofu.json"):
		return true
	case strings.HasSuffix(name, ".hcl"):
		return name != ".terraform.lock.hcl" // .tftest.hcl and terragrunt.hcl included
	}
	return false
}

// scanDirs parses every head directory that holds a .tf or .tf.json entry,
// reusing the areas' parse, and records each call.
func (s *callersScan) scanDirs(parsed map[string]*Module, lim Limits) error {
	for _, dir := range slices.Sorted(maps.Keys(s.head.Dirs)) {
		if !slices.ContainsFunc(s.head.DirEntries(dir), func(e *Entry) bool { return configName(e.Path) }) {
			continue
		}
		m := parsed[dir]
		if m == nil {
			var err error
			if m, err = ParseDir(s.head, dir, lim); err != nil {
				return err
			}
		}
		if len(m.Unsupported) > 0 {
			s.complete = false
		}
		for _, c := range m.ModuleCalls {
			if err := s.call(dir, c); err != nil {
				return err
			}
		}
	}
	return nil
}

// call resolves one module call in dir and records it when it names an area
// that gets an entry.
func (s *callersScan) call(dir string, c ModuleCall) error {
	src := c.Source
	switch {
	case strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../"):
	case src != "" && remoteSource.MatchString(src):
		return nil
	default:
		// Empty, not a literal, absolute, a drive letter or unknown.
		s.complete = false
		return nil
	}
	target := path.Join(dir, src)
	if target == ".." || strings.HasPrefix(target, "../") || !s.targets[target] {
		s.complete = false // escapes the tree, or names no area
		return nil
	}
	if !s.wanted[target] {
		return nil
	}
	if s.records++; s.records > maxModuleCallers {
		return fmt.Errorf("%w: more than %d local module calls", ErrCallerLimit, maxModuleCallers)
	}
	file := path.Join(dir, c.File)
	area, _ := AreaOf(file, s.mods)
	s.byModule[target] = append(s.byModule[target], ModuleCaller{Area: area, File: file, Line: c.SourceRange.Start.Line, FileChanged: s.fileChanged(file)})
	return nil
}

// fileChanged reports whether the change adds the file, changes its bytes,
// or, for a diff, lists it: a file the diff lists has its own base entry.
func (s *callersScan) fileChanged(file string) bool {
	h, b := s.head.Entries[file], s.base.Entries[file]
	return !sameEntry(h, b) || s.change == ChangeDiff && h != b
}

// inRepoDowngrade lists the modules of mcs whose every caller the change
// also touches, when the profile says all consumers are in this repository,
// the run has a change and is not a full-tree run, and the scan is complete.
func inRepoDowngrade(mcs []ModuleCallers, p *profile.Profile, change string, fullTree bool) []string {
	out := []string{}
	if p == nil || p.Options.Consumers != profile.ConsumersInRepo || change == ChangeNone || fullTree {
		return out
	}
	for _, m := range mcs {
		if m.Complete && len(m.Callers) > 0 && !slices.ContainsFunc(m.Callers, func(c ModuleCaller) bool { return !c.FileChanged }) {
			out = append(out, m.Module)
		}
	}
	return out
}

// markDowngrade sets the in-repo downgrade on each candidate of a downgrade
// rule in an area of areas.
func markDowngrade(cs []Candidate, areas, mods []string) {
	for i := range cs {
		a, ok := AreaOf(cs[i].File, mods)
		if ok && slices.Contains(areas, a) && slices.Contains(profile.DowngradeRules, cs[i].RuleID) {
			cs[i].Downgrade, cs[i].Severities = DowngradeInRepo, []string{"MEDIUM"}
		}
	}
}
