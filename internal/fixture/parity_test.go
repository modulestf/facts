package fixture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/behaviour"
	"github.com/modulestf/facts/internal/facts"
)

// TestClassifierCasesBuild holds the classifier to the builder: a trigger
// that lists L1 cases builds, and one whose reference has no input states
// is one the builder refuses. A case list the builder refuses would only
// fail the level; a reference the builder stands in for that the
// classifier refuses would lose evidence.
func TestClassifierCasesBuild(t *testing.T) {
	refs := map[string]bool{
		"aws_s3_bucket.this[*].id":                     true,
		"aws_s3_bucket.this[0].id":                     true,
		"aws_s3_bucket.this.id":                        true,
		"aws_s3_bucket.this.*.id":                      true, // the legacy splat
		"data.aws_iam_policy_document.this[0].id":      true,
		"aws_s3_bucket.this[*].arn":                    false, // no attribute type without a schema
		"data.aws_iam_policy_document.this[*].json":    false,
		"[for b in aws_s3_bucket.this : b.bucket]":     false, // a whole resource
		"aws_s3_bucket.this[0].tags.Name":              false, // a nested attribute
		"aws_s3_bucket.this[\"a\"].id":                 false, // a key index
		"resource.aws_s3_bucket.this[*].id":            false, // the long form
		"path.module":                                  false,
		"terraform.workspace":                          false,
		"local.ids":                                    false,
		"aws_s3_bucket.this[*].id[0]":                  false, // an index after the attribute
		"one(aws_s3_bucket.this[*].id)":                true,
		"try(aws_s3_bucket.this[0].tags.Name, \"\")":   false,
		"[for k, v in data.aws_s3_bucket.this : v.id]": false,
	}
	tmpl, err := os.ReadFile(filepath.Join(buildTD, "templates", "l1.tf.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	for ref, want := range refs {
		r := classify(t, "locals {\n  x = ["+ref+"]\n}\n", "locals {\n  x = compact(["+ref+"])\n}\n")
		if len(r.Triggers) != 1 {
			t.Fatalf("%s: %d triggers", ref, len(r.Triggers))
		}
		tr := r.Triggers[0]
		trig, err := json.Marshal(tr)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Build(BuildOptions{Level: "L1", Template: tmpl, Trigger: trig})
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if got := len(tr.Cases) > 0; got != want {
			t.Errorf("%s: cases %v, want %v (%s)", ref, got, want, reason(tr.Reason))
		}
		if len(tr.Cases) > 0 && !b.OK {
			t.Errorf("%s: %d cases, the builder refuses: %v", ref, len(tr.Cases), b.Result["reason"])
		}
		if len(tr.Cases) == 0 && b.OK {
			t.Errorf("%s: no cases (%s), the builder builds", ref, reason(tr.Reason))
		}
	}
}

func classify(t *testing.T, base, head string) *behaviour.Report {
	t.Helper()
	dirs := map[string]string{}
	for side, text := range map[string]string{"base": base, "head": head} {
		d := filepath.Join(t.TempDir(), side)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "main.tf"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		dirs[side] = d
	}
	r, err := behaviour.Run(behaviour.Options{Head: dirs["head"], Base: dirs["base"], Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func reason(p *string) string {
	if p == nil {
		return "no reason"
	}
	return strings.TrimSpace(*p)
}
