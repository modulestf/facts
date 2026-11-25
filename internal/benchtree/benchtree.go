// Package benchtree writes a generated Terraform module tree for the
// benchmarks. The tree has the size and shape of a large upstream module:
// a root module, submodules under modules/ and examples under examples/,
// about 90 .tf files and 500 KB in all. The content is deterministic, so
// two runs of a benchmark read the same bytes.
//
// Only tests import this package.
package benchtree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Shape of the tree. Root and every submodule have the files of
// moduleFiles; every example has the files of exampleFiles.
const (
	submodules         = 9
	examples           = 10
	resourcesPerModule = 14
	variablesPerModule = 40
	outputsPerModule   = 24
)

// Changed lists the files Write changes between the base and the head
// tree, in path order.
var Changed = []string{"main.tf", "modules/sub0/main.tf", "variables.tf"}

// Write writes the head tree, or the base tree when base is true, under
// dir. The base differs from the head in the files of Changed only: one
// argument less in a resource of the root module and of modules/sub0, and
// another default of one root variable.
func Write(dir string, base bool) error {
	files := map[string]string{}
	addModule(files, ".", base)
	for i := range submodules {
		addModule(files, fmt.Sprintf("modules/sub%d", i), base && i == 0)
	}
	for i := range examples {
		addExample(files, fmt.Sprintf("examples/ex%d", i), i)
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// addModule adds main.tf, variables.tf, outputs.tf, versions.tf and
// locals.tf of one module directory. base drops the changed lines.
func addModule(files map[string]string, dir string, base bool) {
	files[join(dir, "main.tf")] = moduleMain(dir, base)
	files[join(dir, "variables.tf")] = moduleVariables(dir == "." && base)
	files[join(dir, "outputs.tf")] = moduleOutputs()
	files[join(dir, "versions.tf")] = versions
	files[join(dir, "locals.tf")] = moduleLocals()
}

func join(dir, name string) string {
	if dir == "." {
		return name
	}
	return dir + "/" + name
}

const versions = `terraform {
  required_version = ">= 1.5.7"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = ">= 4.0"
    }
  }
}
`

func moduleMain(dir string, base bool) string {
	var b strings.Builder
	b.WriteString("data \"aws_partition\" \"current\" {\n  count = var.create ? 1 : 0\n}\n\n")
	b.WriteString("data \"aws_caller_identity\" \"current\" {\n  count = var.create ? 1 : 0\n}\n\n")
	for i := range resourcesPerModule {
		writeResource(&b, i, base && i == 0)
		writePolicy(&b, i)
	}
	if dir == "." {
		for i := range submodules {
			fmt.Fprintf(&b, `module "sub%[1]d" {
  source = "./modules/sub%[1]d"

  create      = var.create && var.enable_sub%[1]d
  name        = "${var.name}-sub%[1]d"
  description = var.description
  rules_0     = var.rules_0
  settings_0  = var.settings_0
  tags        = merge(var.tags, { Submodule = "sub%[1]d" })
}

`, i)
		}
	}
	return b.String()
}

// writeResource writes one resource with plain, nested and dynamic blocks.
// dropped leaves out one argument, the change between base and head.
func writeResource(b *strings.Builder, i int, dropped bool) {
	fmt.Fprintf(b, `resource "aws_security_group" "r%[1]d" {
  count = var.create ? 1 : 0

  name                   = "${var.name}-r%[1]d"
  name_prefix            = var.use_name_prefix ? "${var.name}-" : null
  description            = coalesce(var.description, "Security group r%[1]d")
  vpc_id                 = var.vpc_id
  revoke_rules_on_delete = var.revoke_rules_on_delete
`, i)
	if !dropped {
		fmt.Fprintf(b, "  ingress_limit          = try(var.settings_%d.limit, 10)\n", i%4)
	}
	fmt.Fprintf(b, `
  dynamic "ingress" {
    for_each = var.rules_%[1]d

    content {
      description      = try(ingress.value.description, null)
      from_port        = ingress.value.from_port
      to_port          = ingress.value.to_port
      protocol         = ingress.value.protocol
      cidr_blocks      = lookup(ingress.value, "cidr_blocks", null)
      ipv6_cidr_blocks = lookup(ingress.value, "ipv6_cidr_blocks", null)
      self             = try(ingress.value.self, false)
    }
  }

  dynamic "egress" {
    for_each = { for k, v in var.rules_%[1]d : k => v if try(v.egress, false) }

    content {
      from_port   = egress.value.from_port
      to_port     = egress.value.to_port
      protocol    = egress.value.protocol
      cidr_blocks = egress.value.cidr_blocks
    }
  }

  timeouts {
    create = try(var.timeouts.create, "10m")
    delete = try(var.timeouts.delete, "15m")
  }

  tags = merge(
    var.tags,
    local.common_tags,
    { Name = "${var.name}-r%[1]d", Index = "%[1]d" },
  )

  lifecycle {
    create_before_destroy = true
    ignore_changes        = [tags["CreatedAt"]]
  }
}

`, i%4)
}

func writePolicy(b *strings.Builder, i int) {
	fmt.Fprintf(b, `data "aws_iam_policy_document" "p%[1]d" {
  count = var.create ? 1 : 0

  statement {
    sid       = "Allow%[1]d"
    effect    = "Allow"
    actions   = ["ec2:DescribeSecurityGroups", "ec2:DescribeVpcs", "logs:CreateLogStream"]
    resources = ["arn:${data.aws_partition.current[0].partition}:ec2:*:${data.aws_caller_identity.current[0].account_id}:security-group/*"]

    condition {
      test     = "StringEquals"
      variable = "aws:RequestedRegion"
      values   = var.allowed_regions
    }
  }

  dynamic "statement" {
    for_each = var.extra_statements

    content {
      sid       = statement.value.sid
      actions   = statement.value.actions
      resources = statement.value.resources
    }
  }
}

resource "aws_iam_policy" "p%[1]d" {
  count = var.create && var.create_policy ? 1 : 0

  name        = "${var.name}-p%[1]d"
  description = "Policy p%[1]d of ${var.name}"
  policy      = data.aws_iam_policy_document.p%[1]d[0].json
  tags        = var.tags
}

`, i)
}

func moduleVariables(base bool) string {
	var b strings.Builder
	createDefault := "true"
	if base {
		createDefault = "false"
	}
	fmt.Fprintf(&b, "variable \"create\" {\n  description = \"Whether to create the resources of this module\"\n  type        = bool\n  default     = %s\n}\n\n", createDefault)
	for _, v := range []string{"name", "description", "vpc_id"} {
		fmt.Fprintf(&b, "variable %q {\n  description = \"The %s of the resources\"\n  type        = string\n  default     = null\n}\n\n", v, v)
	}
	for _, v := range []string{"use_name_prefix", "revoke_rules_on_delete", "create_policy"} {
		fmt.Fprintf(&b, "variable %q {\n  description = \"Whether to set %s\"\n  type        = bool\n  default     = false\n}\n\n", v, v)
	}
	for i := range submodules {
		fmt.Fprintf(&b, "variable \"enable_sub%d\" {\n  description = \"Whether to create submodule %d\"\n  type        = bool\n  default     = true\n}\n\n", i, i)
	}
	b.WriteString(`variable "tags" {
  description = "A map of tags to add to all resources"
  type        = map(string)
  default     = {}
}

variable "allowed_regions" {
  description = "Regions the policies allow"
  type        = list(string)
  default     = ["eu-west-1", "us-east-1"]
}

variable "extra_statements" {
  description = "Extra policy statements"
  type = list(object({
    sid       = string
    actions   = list(string)
    resources = list(string)
  }))
  default = []
}

variable "timeouts" {
  description = "Create and delete timeouts"
  type        = map(string)
  default     = {}
}

`)
	for i := range 4 {
		fmt.Fprintf(&b, `variable "rules_%[1]d" {
  description = "Ingress rules of group set %[1]d"
  type = map(object({
    description      = optional(string)
    from_port        = number
    to_port          = number
    protocol         = optional(string, "tcp")
    cidr_blocks      = optional(list(string))
    ipv6_cidr_blocks = optional(list(string))
    self             = optional(bool, false)
    egress           = optional(bool, false)
  }))
  default = {}

  validation {
    condition     = alltrue([for r in values(var.rules_%[1]d) : r.from_port <= r.to_port])
    error_message = "Each rule needs from_port <= to_port."
  }
}

variable "settings_%[1]d" {
  description = "Settings of group set %[1]d"
  type = object({
    limit   = optional(number, 10)
    enabled = optional(bool, true)
    labels  = optional(map(string), {})
  })
  default  = {}
  nullable = false
}

`, i)
	}
	// The variables above are 19 plus one per submodule.
	for i := 19 + submodules; i < variablesPerModule; i++ {
		fmt.Fprintf(&b, "variable \"extra_%d\" {\n  description = \"Extra setting %d, passed through as it is\"\n  type        = any\n  default     = null\n}\n\n", i, i)
	}
	return b.String()
}

func moduleOutputs() string {
	var b strings.Builder
	for i := range outputsPerModule {
		r := i % resourcesPerModule
		if i < resourcesPerModule {
			fmt.Fprintf(&b, "output \"r%[1]d_id\" {\n  description = \"The ID of security group r%[1]d\"\n  value       = try(aws_security_group.r%[1]d[0].id, null)\n}\n\n", r)
			continue
		}
		fmt.Fprintf(&b, "output \"p%[1]d_arn\" {\n  description = \"The ARN of policy p%[1]d\"\n  value       = try(aws_iam_policy.p%[1]d[0].arn, null)\n}\n\n", r)
	}
	return b.String()
}

func moduleLocals() string {
	var b strings.Builder
	b.WriteString("locals {\n  common_tags = {\n    Module    = \"benchtree\"\n    Terraform = \"true\"\n  }\n\n")
	for i := range resourcesPerModule {
		fmt.Fprintf(&b, "  group_%[1]d_ids = [for k, v in var.rules_%[2]d : k if try(v.self, false)]\n", i, i%4)
	}
	b.WriteString("}\n")
	return b.String()
}

// addExample adds main.tf, variables.tf, outputs.tf and versions.tf of one
// example. Every example calls the root module; example n also calls
// submodule n modulo the submodule count.
func addExample(files map[string]string, dir string, n int) {
	var main strings.Builder
	fmt.Fprintf(&main, `provider "aws" {
  region = local.region
}

locals {
  name   = "ex%[1]d-${basename(path.cwd)}"
  region = "eu-west-1"

  tags = {
    Example    = local.name
    Repository = "benchtree"
  }
}

module "root" {
  source = "../../"

  name   = local.name
  vpc_id = "vpc-0123456789abcdef0"

  rules_0 = {
    https = { from_port = 443, to_port = 443, cidr_blocks = ["10.0.0.0/8"] }
    http  = { from_port = 80, to_port = 80, cidr_blocks = ["10.0.0.0/8"] }
  }

  tags = local.tags
}

module "sub" {
  source = "../../modules/sub%[2]d"

  name = "${local.name}-sub"
  tags = local.tags
}

module "disabled" {
  source = "../../"

  create = false
}

`, n, n%submodules)
	for i := range 40 {
		fmt.Fprintf(&main, "resource \"aws_ssm_parameter\" \"p%[1]d\" {\n  name  = \"/${local.name}/p%[1]d\"\n  type  = \"String\"\n  value = module.root.r%[2]d_id\n  tags  = local.tags\n}\n\n", i, i%resourcesPerModule)
	}
	files[dir+"/main.tf"] = main.String()
	files[dir+"/variables.tf"] = "variable \"region\" {\n  description = \"Region of the example\"\n  type        = string\n  default     = \"eu-west-1\"\n}\n"
	var out strings.Builder
	for i := range resourcesPerModule {
		fmt.Fprintf(&out, "output \"r%[1]d_id\" {\n  description = \"The ID of security group r%[1]d\"\n  value       = module.root.r%[1]d_id\n}\n\n", i)
	}
	files[dir+"/outputs.tf"] = out.String()
	files[dir+"/versions.tf"] = versions
}
