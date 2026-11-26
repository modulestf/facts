package schemadiff

import (
	"path/filepath"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// TestKeepTypesCoversLookups: every type Run looks up, a resource and a
// data source among them, is a type the schema files keep, and another type
// is not. A type not kept reads as schema_unavailable, which Run reports as
// a provider the file lacks.
func TestKeepTypesCoversLookups(t *testing.T) {
	types, err := ModuleTypes(filepath.Join(testdata, "module"), facts.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	keep := keepTypes(types)
	modes := map[string]bool{}
	for _, u := range types {
		if !keep(u.Mode == "data", u.Type.Type) {
			t.Errorf("%s %s is looked up but not kept", u.Mode, u.Type.Type)
		}
		if keep(u.Mode != "data", u.Type.Type) {
			t.Errorf("%s %s is kept in the other mode too", u.Mode, u.Type.Type)
		}
		modes[u.Mode] = true
	}
	if !modes["resource"] || !modes["data"] {
		t.Errorf("modes %v: the fixture must use a resource and a data source", modes)
	}
	if keep(false, "aws_not_in_the_module") {
		t.Error("a type the module does not use is kept")
	}
}
