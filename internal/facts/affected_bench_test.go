package facts

import "testing"

func BenchmarkAffected(b *testing.B) {
	for _, tr := range benchTrees(b) {
		b.Run(tr.name, func(b *testing.B) {
			o := AffectedOptions{Head: tr.head, Base: tr.base, Limits: DefaultLimits}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Affected(o); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
