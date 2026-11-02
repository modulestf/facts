package depth

import (
	"path"
	"strings"

	"github.com/modulestf/facts/internal/behaviour"
)

// Arguments a classifier trigger may not change at a focused depth.
var metaArgs = map[string]bool{
	"count": true, "for_each": true, "depends_on": true, "provider": true, "providers": true,
	"lifecycle": true, "source": true, "version": true, "provisioner": true, "connection": true,
}

// allowed applies the classifier allow-list of D-4 to every trigger: an
// expression or literal change in a resource or data argument, a local, an
// output value or a module call argument; an unknown propagation candidate;
// an example variables file. The address must name a block of that kind in
// the trigger's directory at either revision.
func allowed(rep *behaviour.Report, head, base *model) bool {
	if rep.Capped > 0 {
		return false
	}
	for _, t := range rep.Triggers {
		for _, cat := range append([]string{t.Category}, t.AlsoCategories...) {
			if !allowedTrigger(t, cat, head, base) {
				return false
			}
		}
	}
	return true
}

func allowedTrigger(t behaviour.Trigger, cat string, head, base *model) bool {
	switch cat {
	case behaviour.CatUnknownPropagation:
		return true
	case behaviour.CatNonHCL:
		return strings.HasPrefix(t.File, "examples/") && tfForm(t.File) == formVars
	case behaviour.CatLiteral, behaviour.CatWrapper, behaviour.CatUnclassified, behaviour.CatLocalsOutputs:
	default:
		return false
	}
	if t.Address == nil {
		return false
	}
	kind := ""
	for _, m := range []*model{head, base} {
		if mod := m.mods[path.Dir(t.File)]; mod != nil {
			if it := mod.byAddr[*t.Address]; it != nil {
				kind = it.kind
			}
		}
	}
	attr := ""
	if t.Attribute != nil {
		attr, _, _ = strings.Cut(*t.Attribute, ".")
	}
	switch kind {
	case kLocal:
		return true
	case kOutput:
		return attr == "" || attr == "value"
	case kResource, kData, kModule:
		return cat != behaviour.CatLocalsOutputs && attr != "" && !metaArgs[attr] && !strings.HasPrefix(attr, "dynamic")
	}
	return false
}
