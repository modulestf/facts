package facts

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/benchtree"
)

// benchCase is one lint run of BenchmarkRun.
type benchCase struct {
	name string
	o    Options
}

// BenchmarkRun runs lint on each tree with no change, with the base tree
// and with the base tree and the test profile, on the small schema case
// with its handoff, and on the large tree with a generated handoff of tens
// of MB (see benchtree.WriteHandoff).
func BenchmarkRun(b *testing.B) {
	schema := "../../testdata/coverage-schema-positive"
	cases := []benchCase{{
		name: "small-schema",
		o: Options{
			Head: filepath.Join(schema, "head"), Base: filepath.Join(schema, "base"),
			SchemaHandoff: filepath.Join(schema, "schema-handoff.json"),
			HeadSHA:       oracleHeadSHA, MergeBaseSHA: oracleBaseSHA,
		},
	}}
	trees := benchTrees(b)
	for _, tr := range trees {
		cases = append(cases,
			benchCase{tr.name + "-no-change", Options{Head: tr.head}},
			benchCase{tr.name + "-base", Options{Head: tr.head, Base: tr.base}},
			benchCase{tr.name + "-profile", Options{Head: tr.head, Base: tr.base, ProfileDir: testProfile}},
		)
	}
	large := trees[len(trees)-1]
	handoff := filepath.Join(b.TempDir(), "schema-handoff.json.gz")
	if err := benchtree.WriteHandoff(handoff); err != nil {
		b.Fatal(err)
	}
	cases = append(cases, benchCase{"large-schema", Options{
		Head: large.head, Base: large.base, SchemaHandoff: handoff,
		HeadSHA: benchtree.HeadSHA, MergeBaseSHA: benchtree.BaseSHA,
	}})
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			o := c.o
			o.Limits = DefaultLimits
			if !checked[c.name] {
				requireInputs(b, o)
				checked[c.name] = true
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Run(o); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// checked holds the cases requireInputs passed in this process, so a run
// with -count checks each case once.
var checked = map[string]bool{}

// requireInputs fails the benchmark when the schema handoff of o does not
// bind to its trees or its profile is not read, so a case never measures a
// run that skips an input it names.
func requireInputs(b *testing.B, o Options) {
	b.Helper()
	r, err := Run(o)
	if err != nil {
		b.Fatal(err)
	}
	if o.ProfileDir != "" && r.Envelope.ProfileSHA256 == nil {
		b.Fatal("the profile is not read")
	}
	if o.SchemaHandoff == "" {
		return
	}
	if r.SchemaCoverage == nil || len(*r.SchemaCoverage) == 0 {
		b.Fatal("the schema handoff gives no coverage")
	}
	for _, m := range r.Manifest {
		if strings.Contains(m.Reason, "schema_unavailable") {
			b.Fatalf("the schema handoff does not bind: %s", m.Reason)
		}
	}
}
