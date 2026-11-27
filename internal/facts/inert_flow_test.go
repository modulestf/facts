package facts

import (
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// TestReadSitesIAMPathFlow checks that an IAM path literal is no read site
// when it goes, through locals, variables and calls of modules the tree
// holds, only to the IAM path of aws_iam_* blocks, and stays one wherever it
// may go anywhere else.
func TestReadSitesIAMPathFlow(t *testing.T) {
	t.Parallel()
	role := func(expr string) string {
		return "resource \"aws_iam_role\" \"r\" {\n  path = " + expr + "\n}\n"
	}
	child := "variable \"p\" {\n  type    = string\n  default = null\n}\n" + role("var.p")
	call := func(src, arg string) string {
		return "module \"m\" {\n  source = \"" + src + "\"\n  p      = " + arg + "\n}\n"
	}
	var chain strings.Builder
	chain.WriteString("locals {\n  l0 = \"/\"\n")
	for i := 1; i <= maxFlowSteps; i++ {
		fmt.Fprintf(&chain, "  l%d = local.l%d\n", i, i-1)
	}
	fmt.Fprintf(&chain, "}\n%s", role(fmt.Sprintf("local.l%d", maxFlowSteps)))
	cases := map[string]struct {
		files map[string]string
		path  string // the file to check
		read  bool
	}{
		"a module argument a called module sets as an IAM path": {
			files: map[string]string{"examples/a/main.tf": call("../../modules/m", "\"/\""), "modules/m/main.tf": child},
			path:  "modules/m/README.md",
		},
		"a module argument to the root module": {
			files: map[string]string{"examples/a/main.tf": call("../..", "\"/\""), "main.tf": child},
			path:  "README.md",
		},
		"a local passed to a module": {
			files: map[string]string{"examples/a/main.tf": "locals {\n  p = \"/\"\n}\n" + call("../..", "local.p"), "main.tf": child},
			path:  "README.md",
		},
		"a chain of locals": {
			files: map[string]string{"main.tf": "locals {\n  a = \"/\"\n  b = local.a\n}\n" + role("local.b")},
			path:  "README.md",
		},
		"a variable default": {
			files: map[string]string{"main.tf": "variable \"p\" {\n  default = \"/\"\n}\nresource \"aws_iam_policy\" \"x\" {\n  path = var.p\n}\n"},
			path:  "README.md",
		},
		"a data source path_prefix": {
			files: map[string]string{"main.tf": "variable \"p\" {\n  default = \"/\"\n}\ndata \"aws_iam_roles\" \"all\" {\n  path_prefix = var.p\n}\n"},
			path:  "README.md",
		},
		"a module that calls itself": {
			files: map[string]string{"main.tf": "variable \"p\" {\n  default = \"/\"\n}\n" + call("./", "var.p") + role("var.p")},
			path:  "README.md",
		},
		"a variable nobody reads": {
			files: map[string]string{"examples/a/main.tf": call("../../modules/m", "\"/\""), "modules/m/main.tf": "variable \"p\" {}\n"},
			path:  "modules/m/README.md",
		},
		"an IAM path of several segments": {
			files: map[string]string{"main.tf": role("\"/service-role/\""), "service-role/a.json": "{}"},
			path:  "service-role/a.json",
		},
		"what replace replaces": {
			files: map[string]string{"main.tf": "locals {\n  n = replace(var.name, \"/\", \"-\")\n}\n"},
			path:  "README.md",
		},
		"the separator of split": {
			files: map[string]string{"main.tf": "locals {\n  n = split(\"/\", var.name)\n}\n"},
			path:  "README.md",
		},
		"the prefix trimprefix removes": {
			files: map[string]string{"main.tf": "locals {\n  n = trimprefix(var.name, \"/\")\n}\n"},
			path:  "README.md",
		},
		"what strcontains looks for": {
			files: map[string]string{"main.tf": "locals {\n  n = strcontains(var.name, \"/\")\n}\n"},
			path:  "README.md",
		},
		"a template replace replaces": {
			files: map[string]string{"main.tf": "locals {\n  n = replace(var.name, \"${var.x}/\", \"-\")\n}\n"},
			path:  "README.md",
		},
		"a heredoc replace replaces": {
			files: map[string]string{"main.tf": "locals {\n  n = replace(var.name, <<-EOT\n    /\n  EOT\n  , \"-\")\n}\n"},
			path:  "README.md",
		},

		"a reference to var alone": {
			files: map[string]string{"main.tf": "locals {\n  a = \"/\"\n  v = [for x in var.l : x]\n}\n" + role("local.a")},
			path:  "README.md",
		},
		"what replace removes as a whole template": {
			files: map[string]string{"main.tf": "locals {\n  n = replace(var.name, \"${path.module}\", \"\")\n}\n"},
			path:  "README.md",
		},
		"a module argument the called module also joins": {
			files: map[string]string{"examples/a/main.tf": call("../../modules/m", "\"/\""),
				"modules/m/main.tf": child + "resource \"aws_s3_object\" \"o\" {\n  source = join(\"\", [path.module, var.p, \"README.md\"])\n}\n"},
			path: "modules/m/README.md", read: true,
		},
		"a module argument another resource reads": {
			files: map[string]string{"examples/a/main.tf": call("../../modules/m", "\"/\""),
				"modules/m/main.tf": child + "resource \"aws_s3_object\" \"o\" {\n  source = var.p\n}\n"},
			path: "modules/m/README.md", read: true,
		},
		"a module argument a called module outputs": {
			files: map[string]string{"examples/a/main.tf": call("../../modules/m", "\"/\""),
				"modules/m/main.tf": child + "output \"p\" {\n  value = var.p\n}\n"},
			path: "modules/m/README.md", read: true,
		},
		"a module the tree does not hold": {
			files: map[string]string{"examples/a/main.tf": call("../../modules/gone", "\"/\"")},
			path:  "README.md", read: true,
		},
		"a module outside the tree": {
			files: map[string]string{"examples/a/main.tf": call("../../../x", "\"/\"")},
			path:  "README.md", read: true,
		},
		"a module from the registry": {
			files: map[string]string{"examples/a/main.tf": call("terraform-aws-modules/iam/aws", "\"/\"")},
			path:  "README.md", read: true,
		},
		"a module meta-argument": {
			files: map[string]string{"examples/a/main.tf": "module \"m\" {\n  source = \"../../modules/m\"\n  count  = \"/\"\n}\n", "modules/m/main.tf": child},
			path:  "README.md", read: true,
		},
		"an IAM block read by path": {
			files: map[string]string{"main.tf": "variable \"p\" {\n  default = \"/\"\n}\n" + role("var.p") + "output \"o\" {\n  value = aws_iam_role.r.path\n}\n"},
			path:  "README.md", read: true,
		},
		"a local read by key": {
			files: map[string]string{"main.tf": "locals {\n  p = \"/\"\n}\n" + role("local.p[\"k\"]")},
			path:  "README.md", read: true,
		},
		"a local inside a nested block": {
			files: map[string]string{"main.tf": "resource \"null_resource\" \"n\" {\n  locals {\n    p = \"/\"\n  }\n}\n" + role("local.p")},
			path:  "README.md", read: true,
		},
		"an IAM path inside a nested block": {
			files: map[string]string{"main.tf": "resource \"null_resource\" \"n\" {\n  resource \"aws_iam_role\" \"r\" {\n    path = \"/\"\n  }\n}\n"},
			path:  "README.md", read: true,
		},
		"a module with no source": {
			files: map[string]string{"main.tf": "module \"m\" {\n  p = \"/\"\n}\n" + child},
			path:  "README.md", read: true,
		},
		"a module with a dynamic source": {
			files: map[string]string{"examples/a/main.tf": call("../../${var.m}", "\"/\""), "main.tf": child},
			path:  "README.md", read: true,
		},
		"a module source that is no local path": {
			files: map[string]string{"main.tf": call("modules/m", "\"/\""), "modules/m/main.tf": child},
			path:  "README.md", read: true,
		},
		"a resource with a source argument": {
			files: map[string]string{"main.tf": "resource \"null_resource\" \"n\" {\n  source = \"./modules/m\"\n  p      = \"/\"\n}\n", "modules/m/main.tf": child},
			path:  "README.md", read: true,
		},
		"a default of a block that is no variable": {
			files: map[string]string{"main.tf": "resource \"p\" \"n\" {\n  default = \"/\"\n}\n" + child},
			path:  "README.md", read: true,
		},
		"a variable argument other than default": {
			files: map[string]string{"main.tf": "variable \"p\" {\n  description = \"/\"\n}\n" + role("var.p")},
			path:  "README.md", read: true,
		},
		"a variable an IAM path reads inside a call": {
			files: map[string]string{"main.tf": "variable \"p\" {\n  default = \"/\"\n}\n" + role("format(\"%s\", var.p)")},
			path:  "README.md", read: true,
		},
		"a module call an override file may give another source": {
			files: map[string]string{"examples/a/main.tf": call("../../modules/m", "\"/\""), "modules/m/main.tf": child,
				"examples/a/override.tf": "module \"m\" {\n  source = \"terraform-aws-modules/iam/aws\"\n}\n"},
			path: "README.md", read: true,
		},
		"a module call beside a named override file": {
			files: map[string]string{"examples/a/main.tf": call("../../modules/m", "\"/\""), "modules/m/main.tf": child,
				"examples/a/m_override.tf": "locals {}\n"},
			path: "README.md", read: true,
		},
		"an override file in another directory": {
			files: map[string]string{"examples/a/main.tf": call("../../modules/m", "\"/\""), "modules/m/main.tf": child,
				"examples/b/override.tf": "locals {}\n"},
			path: "modules/m/README.md",
		},
		"a chain past the step cap": {
			files: map[string]string{"main.tf": chain.String()},
			path:  "README.md", read: true,
		},
		"a literal that does not end in a slash": {
			files: map[string]string{"main.tf": role("\"/README.md\"")},
			path:  "README.md", read: true,
		},
		"a literal that does not start with a slash": {
			files: map[string]string{"main.tf": role("\"README.md/\"")},
			path:  "README.md", read: true,
		},
		"a literal with a space": {
			files: map[string]string{"main.tf": role("\"/a b/\""), "a b/x.json": "{}"},
			path:  "a b/x.json", read: true,
		},
		"a slash given to fileset": {
			files: map[string]string{"main.tf": "locals {\n  f = fileset(\"/\", \"*.md\")\n}\n"},
			path:  "README.md", read: true,
		},
		"a slash given to file in a variable default": {
			files: map[string]string{"main.tf": "variable \"p\" {\n  default = \"/\"\n}\nlocals {\n  f = file(\"${var.p}README.md\")\n}\n" + role("var.p")},
			path:  "README.md", read: true,
		},
		"the replacement of replace": {
			files: map[string]string{"main.tf": "locals {\n  n = replace(var.name, \"-\", \"/\")\n}\n"},
			path:  "README.md", read: true,
		},
		"the string of trimsuffix": {
			files: map[string]string{"main.tf": "locals {\n  n = trimsuffix(\"/\", var.x)\n}\n"},
			path:  "README.md", read: true,
		},
		"a file read inside what strcontains looks for": {
			files: map[string]string{"main.tf": "locals {\n  n = strcontains(var.name, file(\"${path.module}/README.md\"))\n}\n"},
			path:  "README.md", read: true,
		},
		"replace with too few arguments": {
			files: map[string]string{"main.tf": "locals {\n  n = replace(\"/\")\n}\n"},
			path:  "README.md", read: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{"README.md": "# r\n", "modules/m/README.md": "# m\n"}
			maps.Copy(files, tc.files)
			tree, err := ReadTree(writeTree(t, files), DefaultLimits)
			if err != nil {
				t.Fatal(err)
			}
			sites, complete, err := ReadSites(tree, DefaultLimits)
			if err != nil || !complete {
				t.Fatalf("ReadSites: complete %v, err %v", complete, err)
			}
			if got := ReachedBy(tc.path, sites, exampleRoots(tree)); got != tc.read {
				t.Errorf("%s reached = %v, want %v; sites %+v", tc.path, got, tc.read, sites)
			}
		})
	}
}

// TestIAMPathText checks the shape of an IAM path literal.
func TestIAMPathText(t *testing.T) {
	t.Parallel()
	long := "/" + strings.Repeat("a", maxIAMPath-2) + "/"
	cases := map[string]bool{
		`"/"`: true, `"/service-role/"`: true, `"` + long + `"`: true,
		`"/` + strings.Repeat("a", maxIAMPath-1) + `/"`: false,
		`"/a b/"`: false, `"/a\tb/"`: false, `"/\u00e9/"`: false, `"/a"`: false, `"a/"`: false, `""`: false,
		`"${path.module}/"`: false, `"/${var.x}/"`: false, `1`: false,
	}
	for src, want := range cases {
		e, diags := hclsyntax.ParseExpression([]byte(src), "x.tf", hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("%s: %s", src, diags)
		}
		if got := iamPathText(e); got != want {
			t.Errorf("iamPathText(%.40s) = %v, want %v", src, got, want)
		}
	}
}
