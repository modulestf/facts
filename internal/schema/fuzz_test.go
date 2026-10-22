package schema

import (
	"os"
	"testing"
)

// FuzzHandoff feeds arbitrary bytes to the handoff parser. A refusal is
// fine; a panic is not. An accepted handoff is indexed and looked up, which
// walks every converted block.
func FuzzHandoff(f *testing.F) {
	seeds := []string{"../../schema/examples/schema-handoff.valid.json"}
	for _, name := range oracleHandoffs {
		seeds = append(seeds, oracleHandoff(name))
	}
	for _, p := range seeds {
		data, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		h, _, err := parseHandoff(data)
		if err != nil {
			return
		}
		s := &Set{Handoff: h}
		s.index()
		s.Bind(matching(s))
		for _, d := range h.Directories {
			for _, p := range d.Providers {
				s.Resolve(d.Path, p.LocalName)
			}
		}
		for _, ps := range h.ProviderSchemas {
			for name := range ps.ResourceSchemas {
				s.Lookup(Query{Source: ps.Source, Version: ps.Version, Type: name, Path: []string{"a", "b"}})
			}
		}
	})
}
