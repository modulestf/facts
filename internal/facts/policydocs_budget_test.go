package facts

import "testing"

const budgetDoc = `data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["s3:GetObject"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "p" {
  policy = data.aws_iam_policy_document.x.json
}
`

// The per-run token budget: a parse past it leaves its directory
// undecidable with limit_exceeded, so a comparison is never equal.
func TestPolicyDocsTokenBudget(t *testing.T) {
	files := map[string]string{"main.tf": budgetDoc}
	head, base := writeTree(t, files), writeTree(t, files)
	probe := DefaultLimits.StartRun()
	if _, err := PolicyDocs(PolicyDocsOptions{Head: head, Limits: probe}); err != nil {
		t.Fatal(err)
	}
	one := probe.runTokens.Load()
	if one == 0 {
		t.Fatal("the run charged no tokens")
	}
	limitDir := func(t *testing.T, r *PolicyReport) {
		t.Helper()
		if d := r.Directories[0]; d.Undecidable == nil || d.Undecidable.Code != ReasonLimitExceeded {
			t.Errorf("directory: %+v", d.Undecidable)
		}
	}
	t.Run("the base passes the budget the head left", func(t *testing.T) {
		lim := DefaultLimits
		lim.MaxRunTokens = one + one/2
		r, err := PolicyDocs(PolicyDocsOptions{Head: head, Base: base, Limits: lim.StartRun()})
		if err != nil {
			t.Fatal(err)
		}
		if r.Head.Directories[0].Undecidable != nil {
			t.Fatalf("the head is over the budget too: %+v", r.Head.Directories[0].Undecidable)
		}
		limitDir(t, r.Base)
		if p := policyPair(t, r, ".", "data.aws_iam_policy_document.x"); p.Presence != PresenceUndecidable || p.Content != nil {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("the head alone passes the budget", func(t *testing.T) {
		lim := DefaultLimits
		lim.MaxRunTokens = one - 1
		r, err := PolicyDocs(PolicyDocsOptions{Head: head, Limits: lim.StartRun()})
		if err != nil {
			t.Fatal(err)
		}
		limitDir(t, r.Head)
		for _, d := range r.Head.Documents {
			if d.Content != nil || d.Uses != nil {
				t.Errorf("%s read past the budget", d.Address)
			}
		}
	})
}

// A committed .terraform directory is repository content: a module there is
// read and compared like any other. A .git path is never read, so a source
// into one does not resolve and the comparison is undecidable.
func TestPolicyDocsDotTerraformAndGit(t *testing.T) {
	call := func(src string) string { return "module \"m\" {\n  source = \"" + src + "\"\n}\n" }
	widened := `data "aws_iam_policy_document" "x" {
  statement {
    actions   = ["*"]
    resources = ["*"]
  }
}
resource "aws_iam_policy" "p" {
  policy = data.aws_iam_policy_document.x.json
}
`
	run := func(t *testing.T, head, base map[string]string) *PolicyDocsReport {
		t.Helper()
		r, err := PolicyDocs(PolicyDocsOptions{Head: writeTree(t, head), Base: writeTree(t, base), Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	t.Run("a policy under .terraform changes", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": call("./.terraform/m"), ".terraform/m/main.tf": widened},
			map[string]string{"main.tf": call("./.terraform/m"), ".terraform/m/main.tf": budgetDoc})
		p := policyPair(t, r, ".terraform/m", "data.aws_iam_policy_document.x")
		if p.Presence != PresenceBoth || p.Content == nil || *p.Content != CompareDiffers {
			t.Errorf("pair: %+v", p)
		}
	})
	t.Run("a policy under .terraform with no caller changes", func(t *testing.T) {
		r := run(t, map[string]string{"main.tf": budgetDoc, ".terraform/modules/m/main.tf": widened},
			map[string]string{"main.tf": budgetDoc, ".terraform/modules/m/main.tf": budgetDoc})
		p := policyPair(t, r, ".terraform/modules/m", "data.aws_iam_policy_document.x")
		if p.Content == nil || *p.Content != CompareDiffers {
			t.Errorf("pair: %+v", p)
		}
	})
	for name, src := range map[string]string{"under the root .git": "./.git/m", "under a nested .git": "./n/.git/m"} {
		t.Run("a module source "+name, func(t *testing.T) {
			dir := src[2:]
			files := map[string]string{"main.tf": budgetDoc + call(src), dir + "/main.tf": budgetDoc}
			r := run(t, files, files)
			if p := policyPair(t, r, ".", "data.aws_iam_policy_document.x"); p.Presence != PresenceUndecidable {
				t.Errorf("pair: %+v", p)
			}
			for _, d := range r.Head.Directories {
				if d.Dir == dir {
					t.Errorf("a .git directory was read: %s", d.Dir)
				}
			}
		})
	}
}
