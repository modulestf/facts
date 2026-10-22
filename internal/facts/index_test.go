package facts

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const indexSrc = `terraform {
  required_version = ">= 1.5.7"
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  alias = "replica"
}

provider "aws" {}

variable "settings" {
  type = object({
    mode = optional(string, "fast")
  })
  default = {}
}

locals {
  region = "eu-west-1"
}

data "aws_iam_policy_document" "this" {
  statement {
    actions = ["a"]
  }
  statement {
    actions = ["b"]
  }
}

resource "aws_x" "this" {
  for_each = var.items
  dynamic "rule" {
    for_each = var.rules
    content {
      id = rule.value
    }
  }
}

module "network" {
  source = "org/network/aws"
}

moved {
  from = aws_x.old
  to   = aws_x.this
}

output "id" {
  value = aws_x.this
}
`

// indexOf builds the index of a tree with no base, so every area is in scope.
func indexOf(t *testing.T, files map[string]string) []IndexEntry {
	t.Helper()
	areas, err := FindAreas(mustTree(t, writeTree(t, files)), nil, DefaultLimits, false)
	if err != nil {
		t.Fatal(err)
	}
	return BuildIndex(areas)
}

func TestBuildIndex(t *testing.T) {
	idx := indexOf(t, map[string]string{"main.tf": indexSrc, "modules/worker/main.tf": "locals {\n  x = 1\n}\n"})
	for _, tc := range []struct {
		file string
		line int
		want string
	}{
		{"main.tf", 1, ".:terraform"},
		{"main.tf", 2, ".:required_version"},
		{"main.tf", 3, ".:required_providers"},
		{"main.tf", 5, ".:required_providers.aws"},
		{"main.tf", 10, ".:provider.aws.replica"},
		{"main.tf", 11, ".:provider.aws.replica.alias"},
		{"main.tf", 14, ".:provider.aws"},
		{"main.tf", 16, ".:var.settings"},
		{"main.tf", 18, ".:var.settings.mode"},
		{"main.tf", 20, ".:var.settings"},
		{"main.tf", 23, ".:locals"},
		{"main.tf", 24, ".:local.region"},
		{"main.tf", 27, ".:data.aws_iam_policy_document.this"},
		{"main.tf", 28, ".:data.aws_iam_policy_document.this.statement[0]"},
		{"main.tf", 29, ".:data.aws_iam_policy_document.this.statement[0].actions"},
		{"main.tf", 32, ".:data.aws_iam_policy_document.this.statement[1].actions"},
		{"main.tf", 37, ".:aws_x.this.for_each"},
		{"main.tf", 38, ".:aws_x.this.rule"},
		{"main.tf", 41, ".:aws_x.this.rule.id"},
		{"main.tf", 47, ".:module.network.source"},
		{"main.tf", 50, ".:moved.aws_x.old"},
		{"main.tf", 56, ".:output.id"},
		{"main.tf", 6, ".:required_providers.aws"},
		{"main.tf", 9, ""},
		{"main.tf", 999, ""},
		{"other.tf", 1, ""},
		{"modules/worker/main.tf", 2, "modules/worker:local.x"},
	} {
		if got := SubjectAt(idx, tc.file, tc.line); got != tc.want {
			t.Errorf("%s:%d: got %q, want %q", tc.file, tc.line, got, tc.want)
		}
	}
}

// Of two entries on the same lines the inner wins: a one-line block and the
// argument in it.
func TestSubjectAtInnerWins(t *testing.T) {
	idx := indexOf(t, map[string]string{"main.tf": "resource \"aws_x\" \"a\" { name = \"n\" }\n"})
	if got := SubjectAt(idx, "main.tf", 1); got != ".:aws_x.a.name" {
		t.Errorf("got %q (index %+v)", got, idx)
	}
}

// Only the areas in scope are indexed: with a base, an untouched area has no
// entries even when a cross-area rule parses it.
func TestBuildIndexSkipsUntouched(t *testing.T) {
	files := map[string]string{"main.tf": "locals {\n  a = 1\n}\n", "modules/x/main.tf": "locals {\n  b = 1\n}\n"}
	head, base := writeTree(t, files), writeTree(t, files)
	if err := os.WriteFile(filepath.Join(head, "main.tf"), []byte("locals {\n  a = 2\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	areas, err := FindAreas(mustTree(t, head), mustTree(t, base), DefaultLimits, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range BuildIndex(areas) {
		if strings.HasPrefix(e.File, "modules/") {
			t.Errorf("untouched area indexed: %+v", e)
		}
	}
	if got := SubjectAt(BuildIndex(areas), "main.tf", 2); got != ".:local.a" {
		t.Errorf("got %q", got)
	}
}

// The index resolves every line of every block of the committed test trees,
// the golden cases and the oracle fixtures, to a subject in the right area.
// The blocks are found by a walk of the parser's own syntax tree, apart from
// the model the index is built from.
func TestIndexResolvesTestTrees(t *testing.T) {
	var roots []string
	for _, pattern := range []string{"../../testdata/*/head", "../../testdata/*/base", "../../testdata/oracle/*/head", "../../testdata/oracle/*/base"} {
		m, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, m...)
	}
	if len(roots) == 0 {
		t.Fatal("no test trees")
	}
	checked := 0
	for _, root := range roots {
		tr := mustTree(t, root)
		areas, err := FindAreas(tr, nil, DefaultLimits, false)
		if err != nil {
			t.Fatal(err)
		}
		idx := BuildIndex(areas)
		for _, a := range areas {
			for _, e := range tr.DirEntries(a.Path) {
				if e.Kind != KindFile || !strings.HasSuffix(e.Path, ".tf") || unsupportedName(path.Base(e.Path)) != "" {
					continue
				}
				// A file the engine refuses, such as one over the nesting
				// limit, has no facts and so no index entries.
				scratch := &Module{varNames: map[string]bool{}, outNames: map[string]bool{}}
				if parseFile(scratch, path.Base(e.Path), e.Data, DefaultLimits) != nil {
					continue
				}
				f, diags := hclsyntax.ParseConfig(e.Data, e.Path, hcl.InitialPos)
				if diags.HasErrors() {
					t.Fatalf("%s/%s: the engine parsed what hclsyntax refuses", root, e.Path)
				}
				for _, b := range f.Body.(*hclsyntax.Body).Blocks {
					for line := b.Range().Start.Line; line <= b.Range().End.Line; line++ {
						checked++
						got := SubjectAt(idx, e.Path, line)
						if !strings.HasPrefix(got, a.Path+":") {
							t.Errorf("%s/%s:%d: subject %q, want one in area %s", root, e.Path, line, got, a.Path)
						}
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no block lines checked")
	}
}
