package facts

import (
	"cmp"
	"path"
	"slices"
	"strconv"
	"strings"
)

// How a run was given the change.
const (
	ChangeNone = "none" // no change: every area is new
	ChangeBase = "base" // a base tree
	ChangeDiff = "diff" // a diff the base tree was rebuilt from
)

// Scope says which areas of the head tree a run checked. With a change the
// checked areas are those it touches, or every area with FullTree; the
// calling areas are the other areas that call a module whose versions or
// inputs it touches, listed under Callers; the rest are left out. The three
// lists are disjoint and together hold every area of the head tree and every
// area the change removes.
type Scope struct {
	Change       string   `json:"change"`
	FullTree     bool     `json:"full_tree"`
	AreasChecked []string `json:"areas_checked"`
	AreasCalling []string `json:"areas_calling"`
	AreasLeftOut []string `json:"areas_left_out"`
}

func buildScope(change string, fullTree bool, areas []*Area, touched map[string]bool, callers *[]ChangedModule) *Scope {
	s := &Scope{Change: change, FullTree: fullTree, AreasChecked: []string{}, AreasCalling: []string{}, AreasLeftOut: []string{}}
	calling := map[string]bool{}
	if callers != nil {
		for _, m := range *callers {
			for _, c := range m.Callers {
				calling[c.Area] = true
			}
		}
	}
	for _, a := range areas {
		switch {
		case !a.Untouched:
			s.AreasChecked = append(s.AreasChecked, a.Path)
		case calling[a.Path] && !touched[a.Path]:
			s.AreasCalling = append(s.AreasCalling, a.Path)
		default:
			s.AreasLeftOut = append(s.AreasLeftOut, a.Path)
		}
	}
	return s
}

// ChangedModule is a module whose versions or inputs the change touches, with
// its floors at base and head and every area that calls it by a local path.
// BaseFloors is nil when the module is new.
type ChangedModule struct {
	Module     string   `json:"module"`
	Changed    []string `json:"changed"` // "inputs", "versions"
	BaseFloors []Floor  `json:"base_floors"`
	HeadFloors []Floor  `json:"head_floors"`
	Callers    []Caller `json:"callers"`
}

// Floor is one version floor a module declares: its required_version, or the
// constraints of one required provider, as written.
type Floor struct {
	Address    string  `json:"address"`    // required_version or required_providers.<name>
	Source     *string `json:"source"`     // the provider source address; null for required_version or a source that is not a plain string
	Constraint *string `json:"constraint"` // null when the entry has no version
}

// Caller is one module call of a changed module, with the floors its area
// declares and the structure.version-floor-understated candidates filed in
// that area.
type Caller struct {
	Area       string   `json:"area"`
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Checked    bool     `json:"checked"`
	Floors     []Floor  `json:"floors"`
	Candidates []string `json:"candidates"`
}

// BuildCallers lists every module the change touches whose versions.tf or
// terraform block changed, or whose variables.tf or a variable block changed,
// with the areas that call it. It runs after Evaluate, which links the areas.
func BuildCallers(areas []*Area, ch *ChangeMap, candidates []Candidate) []ChangedModule {
	out := []ChangedModule{}
	for _, m := range areas {
		if m.Untouched || !m.HeadExists || m.Kind == AreaExample {
			continue
		}
		changed := changedSurface(m, ch)
		if len(changed) == 0 {
			continue
		}
		cm := ChangedModule{Module: m.Path, Changed: changed, HeadFloors: floorsOf(m.Head), Callers: []Caller{}}
		if m.BaseExists {
			cm.BaseFloors = floorsOf(m.Base)
		}
		for _, a := range areas {
			if a == m || a.Head == nil {
				continue
			}
			for _, call := range a.Head.ModuleCalls {
				if calledArea(a, call) != m {
					continue
				}
				cm.Callers = append(cm.Callers, Caller{Area: a.Path, File: areaFile(a.Path, call.File), Line: call.SourceRange.Start.Line,
					Checked: !a.Untouched, Floors: floorsOf(a.Head), Candidates: floorCandidates(a, candidates)})
			}
		}
		slices.SortFunc(cm.Callers, func(x, y Caller) int {
			return cmp.Or(strings.Compare(x.Area, y.Area), strings.Compare(x.File, y.File), cmp.Compare(x.Line, y.Line))
		})
		out = append(out, cm)
	}
	return out
}

// changedSurface says what of a module's surface the change touches.
func changedSurface(m *Area, ch *ChangeMap) []string {
	touched := func(file, prefix string) bool {
		if ch.pathChanged(path.Join(m.Path, file)) {
			return true
		}
		for addr, st := range ch.blocks[m.Path] {
			if st != BlockUntouched && (addr == prefix || strings.HasPrefix(addr, prefix+".")) {
				return true
			}
		}
		return false
	}
	var out []string
	if touched("variables.tf", "var") {
		out = append(out, "inputs")
	}
	if touched("versions.tf", "terraform") {
		out = append(out, "versions")
	}
	return out
}

// floorsOf lists the floors a module declares, sorted by address.
func floorsOf(m *Module) []Floor {
	out := []Floor{}
	for key, f := range moduleFloors(m) {
		parts := make([]string, len(f.text))
		for i, t := range f.text {
			if s, err := strconv.Unquote(t); err == nil {
				t = s // a plain string; anything else stays as written
			}
			parts[i] = t
		}
		fl := Floor{Address: f.addr, Constraint: joined(parts)}
		if key != "" && !strings.HasPrefix(key, "?") {
			fl.Source = &key
		}
		out = append(out, fl)
	}
	slices.SortFunc(out, func(x, y Floor) int { return strings.Compare(x.Address, y.Address) })
	return out
}

// floorCandidates are the ids of the version floor candidates filed in a's
// area.
func floorCandidates(a *Area, candidates []Candidate) []string {
	out := []string{}
	for _, c := range candidates {
		if c.RuleID == "structure.version-floor-understated" && itemArea(c.File, a.peers) == a.Path {
			out = append(out, c.CandidateID)
		}
	}
	return out
}
