package fixture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

var testPins = []Pin{{Source: "hashicorp/aws", Version: "6.15.0"}}

const td = "../../testdata/fixture-check"

func TestCheckPositiveL1(t *testing.T) {
	t.Parallel()
	r, err := Check(Options{Dir: filepath.Join(td, "l1")})
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK || len(r.Violations) != 0 || len(r.TreeSHA256) != 64 || r.SchemaVersion != SchemaVersion {
		t.Fatalf("result %+v", r)
	}
}

func TestCheckPositiveL3(t *testing.T) {
	t.Parallel()
	r, err := Check(Options{Dir: filepath.Join(td, "l3"), Level: "L3", Resource: "aws_rds_cluster", Pins: testPins})
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK || len(r.Violations) != 0 {
		t.Fatalf("result %+v", r)
	}
}

// The L2 fixture: a for_each key change verified by a plan-only test. Its
// --expect manifest binds the .tf file; the test file is left to the grammar.
func TestCheckPositiveL2(t *testing.T) {
	t.Parallel()
	m, err := ReadManifest(filepath.Join(td, "l2.expect.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Check(Options{Dir: filepath.Join(td, "l2"), Level: "L2", Pins: testPins, Expect: m})
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK || len(r.Violations) != 0 {
		t.Fatalf("result %+v", r)
	}
}

// aws_db_instance is the second curated type, under the same provider rules.
func TestCheckPositiveL3DBInstance(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := strings.Replace(l3TF, l3Resource, "resource \"aws_db_instance\" \"this\" {\n  vpc_security_group_ids = [terraform_data.sg.output]\n}\nresource \"terraform_data\" \"sg\" {}\n", 1)
	mustDo(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o644))
	r, err := Check(Options{Dir: dir, Level: "L3", Resource: "aws_db_instance", Pins: testPins})
	if err != nil || !r.OK {
		t.Fatalf("%v %+v", err, r)
	}
	r, err = Check(Options{Dir: dir, Level: "L3", Resource: "aws_rds_cluster", Pins: testPins})
	if err != nil || r.OK {
		t.Fatalf("other type under test passes: %v %+v", err, r)
	}
}

// The tree hash covers names, modes and contents, and nothing else.
func TestTreeSHA256(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(filepath.Join(td, "l1", "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	hash := func(files map[string]string, mode os.FileMode) string {
		t.Helper()
		dir := t.TempDir()
		for name, data := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(data), mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(dir, name), mode); err != nil { // umask
				t.Fatal(err)
			}
		}
		r, err := Check(Options{Dir: dir})
		if err != nil || !r.OK {
			t.Fatalf("check: %v %+v", err, r)
		}
		return r.TreeSHA256
	}
	base := hash(map[string]string{"main.tf": string(src)}, 0o644)
	ref, err := Check(Options{Dir: filepath.Join(td, "l1")})
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{}
	for _, name := range []string{"main.tf", "base.tf", "head.tf"} {
		data, err := os.ReadFile(filepath.Join(td, "l1", name))
		if err != nil {
			t.Fatal(err)
		}
		all[name] = string(data)
	}
	if got := hash(all, 0o644); got != ref.TreeSHA256 {
		t.Errorf("same tree, other directory: %s != %s", ref.TreeSHA256, got)
	}
	if hash(map[string]string{"main.tf": string(src)}, 0o644) != base {
		t.Error("hash is not deterministic")
	}
	// Permission bits other than the exec bit, such as a umask leaves, do not count.
	if hash(map[string]string{"main.tf": string(src)}, 0o600) != base || hash(map[string]string{"main.tf": string(src)}, 0o664) != base {
		t.Error("read and write bits change the hash")
	}
	for name, got := range map[string]string{
		"content":  hash(map[string]string{"main.tf": string(src) + "\n"}, 0o644),
		"name":     hash(map[string]string{"other.tf": string(src)}, 0o644),
		"exec bit": hash(map[string]string{"main.tf": string(src)}, 0o755),
		"file":     hash(map[string]string{"main.tf": string(src), "b.tf": ""}, 0o644),
	} {
		if got == base {
			t.Errorf("a change of %s keeps the hash", name)
		}
	}
}

const okTF = "resource \"terraform_data\" \"a\" {}\n"

// l3TF is a valid L3 tree to add one violation to: its terraform block,
// provider block and resource under test.
const (
	l3Terraform = `terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.15.0"
    }
  }
}
`
	l3Provider = `provider "aws" {
  region                      = "us-east-1"
  access_key                  = "fake"
  secret_key                  = "fake"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = "true"
  skip_region_validation      = true
  endpoints {
    rds = "http://127.0.0.1:1"
  }
}
`
	l3Resource = "resource \"aws_rds_cluster\" \"this\" {}\n"
	l3TF       = l3Terraform + l3Provider + l3Resource
)

// l3sub is l3TF with old replaced by repl.
func l3sub(old, repl string) map[string]string {
	if !strings.Contains(l3TF, old) {
		panic(old)
	}
	return map[string]string{"main.tf": strings.Replace(l3TF, old, repl, 1)}
}

type ruleCase struct {
	name  string
	level string // "" is L1
	files map[string]string
	setup func(t *testing.T, dir string)
	rule  string
}

func l3(extra string) map[string]string { return map[string]string{"main.tf": l3TF + extra} }
func l1(src string) map[string]string   { return map[string]string{"main.tf": src} }

func pad(n int) string { return "#" + strings.Repeat("x", n-2) + "\n" }

var ruleCases = []ruleCase{
	{name: "text file", files: map[string]string{"main.tf": okTF, "notes.txt": "x"}, rule: RuleFileNotTF},
	{name: "json config", files: map[string]string{"main.tf": okTF, "x.tf.json": "{}"}, rule: RuleFileNotTF},
	{name: "auto tfvars", files: map[string]string{"main.tf": okTF, "x.auto.tfvars": "a = 1\n", "terraform.tfvars": "a = 1\n"}, rule: RuleFileNotTF},
	{name: "hidden tf", files: map[string]string{"main.tf": okTF, ".x.tf": okTF}, rule: RuleFileNotTF},
	{name: "lock file", files: map[string]string{"main.tf": okTF, ".terraform.lock.hcl": ""}, rule: RuleFileNotTF},
	{name: "override", files: map[string]string{"main.tf": okTF, "override.tf": "locals {}\n"}, rule: RuleFileOverride},
	{name: "suffix override", files: map[string]string{"main.tf": okTF, "x_override.tf": "locals {}\n"}, rule: RuleFileOverride},
	{name: "subdirectory", files: l1(okTF), setup: func(t *testing.T, dir string) {
		mustDo(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	}, rule: RuleFileSubdirectory},
	{name: "terraform dir", files: l1(okTF), setup: func(t *testing.T, dir string) {
		mustDo(t, os.Mkdir(filepath.Join(dir, ".terraform"), 0o755))
	}, rule: RuleFileSubdirectory},
	{name: "symlink", files: l1(okTF), setup: func(t *testing.T, dir string) {
		mustDo(t, os.Symlink("main.tf", filepath.Join(dir, "link.tf")))
	}, rule: RuleFileSymlink},
	{name: "hard link", files: l1(okTF), setup: func(t *testing.T, dir string) {
		mustDo(t, os.Link(filepath.Join(dir, "main.tf"), filepath.Join(dir, "other.tf")))
	}, rule: RuleFileSpecial},
	{name: "fifo", files: l1(okTF), setup: func(t *testing.T, dir string) {
		mustDo(t, syscall.Mkfifo(filepath.Join(dir, "pipe.tf"), 0o644))
	}, rule: RuleFileSpecial},
	{name: "file size", files: map[string]string{"main.tf": okTF, "big.tf": pad(MaxFileBytes + 1)}, rule: RuleLimitFileSize},
	{name: "file count", files: manyFiles(MaxFiles + 1), rule: RuleLimitFileCount},
	{name: "total size", files: map[string]string{"main.tf": okTF, "a.tf": pad(MaxFileBytes), "b.tf": pad(MaxFileBytes),
		"c.tf": pad(MaxFileBytes), "d.tf": pad(MaxFileBytes), "e.tf": pad(MaxFileBytes)}, rule: RuleLimitTotalSize},
	{name: "parse error", files: l1("resource {\n"), rule: RuleHCLParseError},
	{name: "nesting", files: l1("locals {\n  a = " + strings.Repeat("[", 80) + strings.Repeat("]", 80) + "\n}\n"), rule: RuleLimitHCL},

	{name: "data", files: l1(okTF + "data \"terraform_remote_state\" \"x\" {}\n"), rule: RuleBlockDenied},
	{name: "module", files: l1(okTF + "module \"m\" {\n  source = \"./m\"\n}\n"), rule: RuleBlockDenied},
	{name: "moved", files: l1(okTF + "moved {\n  from = terraform_data.b\n  to = terraform_data.a\n}\n"), rule: RuleBlockDenied},
	{name: "import", files: l1(okTF + "import {\n  to = terraform_data.a\n  id = \"x\"\n}\n"), rule: RuleBlockDenied},
	{name: "removed", files: l1(okTF + "removed {\n  from = terraform_data.b\n}\n"), rule: RuleBlockDenied},
	{name: "check", files: l1(okTF + "check \"c\" {}\n"), rule: RuleBlockDenied},
	{name: "ephemeral", files: l1(okTF + "ephemeral \"random_password\" \"p\" {}\n"), rule: RuleBlockDenied},
	{name: "action", files: l1(okTF + "action \"aws_lambda_invoke\" \"a\" {}\n"), rule: RuleBlockDenied},
	{name: "backend", files: l1(okTF + "terraform {\n  backend \"local\" {}\n}\n"), rule: RuleBlockDenied},
	{name: "cloud", files: l1(okTF + "terraform {\n  cloud {}\n}\n"), rule: RuleBlockDenied},
	{name: "provider_meta", files: l1(okTF + "terraform {\n  provider_meta \"aws\" {}\n}\n"), rule: RuleBlockDenied},
	{name: "provisioner", files: l1("resource \"terraform_data\" \"a\" {\n  provisioner \"local-exec\" {\n    command = \"id\"\n  }\n}\n"), rule: RuleBlockDenied},
	{name: "connection", files: l1("resource \"terraform_data\" \"a\" {\n  connection {\n    host = \"h\"\n  }\n}\n"), rule: RuleBlockDenied},
	{name: "dynamic provisioner", files: l1("resource \"terraform_data\" \"a\" {\n  dynamic \"provisioner\" {\n    for_each = []\n    content {}\n  }\n}\n"), rule: RuleBlockDenied},
	{name: "top-level attribute", files: l1(okTF + "a = 1\n"), rule: RuleBlockDenied},

	{name: "required_version", files: l1(okTF + "terraform {\n  required_version = \">= 1.0\"\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "variable ephemeral", files: l1(okTF + "variable \"v\" {\n  ephemeral = true\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "output ephemeral", files: l1(okTF + "output \"o\" {\n  value = 1\n  ephemeral = true\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "action_trigger", files: l1("resource \"terraform_data\" \"a\" {\n  lifecycle {\n    action_trigger {\n      events = [before_create]\n    }\n  }\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "ignore_changes call", files: l1("resource \"terraform_data\" \"a\" {\n  lifecycle {\n    ignore_changes = [file(\"x\")]\n  }\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "provider alias", level: "L3", files: l3sub(`region `, "alias = \"x\"\n  region "), rule: RuleBlockAttributeDenied},
	{name: "provider assume_role", level: "L3", files: l3sub(`  endpoints {`, "  assume_role {}\n  endpoints {"), rule: RuleBlockAttributeDenied},
	{name: "required provider extra key", level: "L3", files: l3sub(`version = "6.15.0"`, "version = \"6.15.0\"\n      configuration_aliases = []"), rule: RuleBlockAttributeDenied},

	{name: "duplicate source", level: "L3", files: l3sub(`source  = "hashicorp/aws"`, "source = \"hashicorp/aws\"\n      source = \"evil/aws\""), rule: RuleProviderNotPinned},
	{name: "nonliteral version", level: "L3", files: l3sub(`version = "6.15.0"`, "version = \"6.15.0\"\n      version = file(\"x\")"), rule: RuleProviderNotPinned},
	{name: "version constraint", level: "L3", files: l3sub(`"6.15.0"`, `"~> 6.0"`), rule: RuleProviderNotPinned},
	{name: "other source", level: "L3", files: l3sub(`"hashicorp/aws"`, `"evil/aws"`), rule: RuleProviderNotPinned},
	{name: "no required_providers", level: "L3", files: l3sub(l3Terraform, ""), rule: RuleProviderNotPinned},

	{name: "aws in L1", files: l1(okTF + "provider \"aws\" {}\n"), rule: RuleProviderDenied},
	{name: "required aws in L1", files: l1(okTF + "terraform {\n  required_providers {\n    aws = {\n      source = \"hashicorp/aws\"\n      version = \"6.15.0\"\n    }\n  }\n}\n"), rule: RuleProviderDenied},
	{name: "google in L3", level: "L3", files: l3("provider \"google\" {}\n"), rule: RuleProviderDenied},
	{name: "provider alias reference", level: "L3", files: l3sub(l3Resource, "resource \"aws_rds_cluster\" \"this\" {\n  provider = aws.x\n}\n"), rule: RuleProviderDenied},
	{name: "provider meta other", files: l1("resource \"terraform_data\" \"a\" {\n  provider = google\n}\n"), rule: RuleProviderDenied},
	{name: "terraform provider argument", files: l1(okTF + "provider \"terraform\" {\n  x = 1\n}\n"), rule: RuleBlockAttributeDenied},

	{name: "skip false", level: "L3", files: l3sub(`skip_region_validation      = true`, `skip_region_validation = false`), rule: RuleProviderValueDenied},
	{name: "skip missing", level: "L3", files: l3sub("  skip_region_validation      = true\n", ""), rule: RuleProviderValueDenied},
	{name: "region missing", level: "L3", files: l3sub("  region                      = \"us-east-1\"\n", ""), rule: RuleProviderValueDenied},
	{name: "endpoints missing", level: "L3", files: l3sub("  endpoints {\n    rds = \"http://127.0.0.1:1\"\n  }\n", ""), rule: RuleProviderValueDenied},
	{name: "provider block missing", level: "L3", files: l3sub(l3Provider, ""), rule: RuleProviderValueDenied},
	{name: "credential reference", level: "L3", files: l3sub(`access_key                  = "fake"`, "access_key = var.k"), rule: RuleProviderValueDenied},
	{name: "credential interpolation", level: "L3", files: l3sub(`access_key                  = "fake"`, `access_key = "${"fake"}"`), rule: RuleProviderValueDenied},
	{name: "imds endpoint", level: "L3", files: l3sub(`"http://127.0.0.1:1"`, `"http://169.254.169.254"`), rule: RuleProviderValueDenied},
	{name: "https endpoint", level: "L3", files: l3sub(`"http://127.0.0.1:1"`, `"https://127.0.0.1:1"`), rule: RuleProviderValueDenied},
	{name: "userinfo endpoint", level: "L3", files: l3sub(`"http://127.0.0.1:1"`, `"http://u@127.0.0.1:1"`), rule: RuleProviderValueDenied},
	{name: "open port endpoint", level: "L3", files: l3sub(`"http://127.0.0.1:1"`, `"http://127.0.0.1:8080"`), rule: RuleProviderValueDenied},
	{name: "no port endpoint", level: "L3", files: l3sub(`"http://127.0.0.1:1"`, `"http://127.0.0.1"`), rule: RuleProviderValueDenied},
	{name: "localhost endpoint", level: "L3", files: l3sub(`"http://127.0.0.1:1"`, `"http://localhost:1"`), rule: RuleProviderValueDenied},
	{name: "nip.io endpoint", level: "L3", files: l3sub(`"http://127.0.0.1:1"`, `"http://127.0.0.1.nip.io:1"`), rule: RuleProviderValueDenied},
	{name: "mapped v4 endpoint", level: "L3", files: l3sub(`"http://127.0.0.1:1"`, `"http://[::ffff:127.0.0.1]:1"`), rule: RuleProviderValueDenied},
	{name: "decimal ip endpoint", level: "L3", files: l3sub(`"http://127.0.0.1:1"`, `"http://2130706433:1"`), rule: RuleProviderValueDenied},
	{name: "service endpoint missing", level: "L3", files: l3sub(`rds = "http://127.0.0.1:1"`, `sts = "http://127.0.0.1:1"`), rule: RuleProviderValueDenied},
	{name: "region malformed", level: "L3", files: l3sub(`"us-east-1"`, `"us-east-1.evil"`), rule: RuleProviderValueDenied},
	{name: "path endpoint", level: "L3", files: l3sub(`"http://127.0.0.1:1"`, `"http://127.0.0.1:1/x"`), rule: RuleProviderValueDenied},
	{name: "query endpoint", level: "L3", files: l3sub(`"http://127.0.0.1:1"`, `"http://[::1]:1/?a=b"`), rule: RuleProviderValueDenied},

	{name: "filename argument", level: "L3", files: l3sub(l3Resource, "resource \"aws_rds_cluster\" \"this\" {\n  filename = \"~/.aws/credentials\"\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "source argument", level: "L3", files: l3sub(l3Resource, "resource \"aws_rds_cluster\" \"this\" {\n  source = \"/etc/passwd\"\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "file suffix argument", level: "L3", files: l3sub(l3Resource, "resource \"aws_rds_cluster\" \"this\" {\n  x {\n    ca_cert_file = \"x\"\n  }\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "path suffix argument", files: l1("resource \"terraform_data\" \"a\" {\n  key_path = \"x\"\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "content_base64 argument", level: "L3", files: l3sub(l3Resource, "resource \"aws_rds_cluster\" \"this\" {\n  content_base64 = \"eA==\"\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "aws resource in L1", files: l1(okTF + "resource \"aws_s3_bucket\" \"b\" {}\n"), rule: RuleResourceTypeDenied},
	{name: "second type in L3", level: "L3", files: l3("resource \"aws_s3_bucket\" \"b\" {}\n"), rule: RuleResourceTypeDenied},
	{name: "no resource under test", level: "L3", files: l3sub(l3Resource, okTF), rule: RuleResourceUnderTestCount},
	{name: "two resources under test", level: "L3", files: l3("resource \"aws_rds_cluster\" \"that\" {}\n"), rule: RuleResourceUnderTestCount},

	{name: "file", files: l1("locals {\n  a = file(\"/etc/passwd\")\n}\n"), rule: RuleExprFunctionDenied},
	{name: "templatefile", files: l1("locals {\n  a = templatefile(\"t\", {})\n}\n"), rule: RuleExprFunctionDenied},
	{name: "fileset", files: l1("locals {\n  a = fileset(\".\", \"*\")\n}\n"), rule: RuleExprFunctionDenied},
	{name: "abspath", files: l1("locals {\n  a = abspath(\"x\")\n}\n"), rule: RuleExprFunctionDenied},
	{name: "pathexpand", files: l1("locals {\n  a = pathexpand(\"~\")\n}\n"), rule: RuleExprFunctionDenied},
	{name: "timestamp", files: l1("locals {\n  a = timestamp()\n}\n"), rule: RuleExprFunctionDenied},
	{name: "uuid", files: l1("locals {\n  a = uuid()\n}\n"), rule: RuleExprFunctionDenied},
	{name: "bcrypt", files: l1("locals {\n  a = bcrypt(\"x\")\n}\n"), rule: RuleExprFunctionDenied},
	{name: "provider function", files: l1("locals {\n  a = provider::aws::arn_parse(\"x\")\n}\n"), rule: RuleExprFunctionDenied},
	{name: "core namespace", files: l1("locals {\n  a = core::abs(1)\n}\n"), rule: RuleExprFunctionDenied},
	{name: "templatestring", files: l1("locals {\n  a = templatestring(\"x\", {})\n}\n"), rule: RuleExprFunctionDenied},
	{name: "function in template", files: l1("locals {\n  a = \"${file(\"x\")}\"\n}\n"), rule: RuleExprFunctionDenied},
	{name: "function in resource", files: l1("resource \"terraform_data\" \"a\" {\n  input = filebase64(\"x\")\n}\n"), rule: RuleExprFunctionDenied},

	{name: "path.module", files: l1("locals {\n  a = path.module\n}\n"), rule: RuleExprReferenceDenied},
	{name: "path in template", files: l1("locals {\n  a = \"${path.cwd}/x\"\n}\n"), rule: RuleExprReferenceDenied},
	{name: "terraform.workspace", files: l1("locals {\n  a = terraform.workspace\n}\n"), rule: RuleExprReferenceDenied},
	{name: "data reference", files: l1("locals {\n  a = data.x.y.id\n}\n"), rule: RuleExprReferenceDenied},
	{name: "module reference", files: l1("locals {\n  a = module.m.o\n}\n"), rule: RuleExprReferenceDenied},
	{name: "lookalike var root", files: l1("locals {\n  a = v\u0430r.x\n}\n"), rule: RuleExprReferenceDenied},
	{name: "lookalike path root", files: l1("locals {\n  a = p\u0430th.module\n}\n"), rule: RuleExprReferenceDenied},
	{name: "unknown root", files: l1("locals {\n  a = aws_s3_bucket.b.id\n}\n"), rule: RuleExprReferenceDenied},
	{name: "for binds path", files: l1("locals {\n  a = [for path in [1] : path]\n}\n"), rule: RuleExprReferenceDenied},
	{name: "for binds var", files: l1("locals {\n  a = {for k, var in {} : k => var}\n}\n"), rule: RuleExprReferenceDenied},
	{name: "template for binds path", files: l1("locals {\n  a = \"%{for path in [1]}${path}%{endfor}\"\n}\n"), rule: RuleExprReferenceDenied},
	{name: "dynamic iterator path", level: "L3", files: l3sub(l3Resource, "resource \"aws_rds_cluster\" \"this\" {\n  dynamic \"x\" {\n    for_each = [1]\n    iterator = path\n    content {}\n  }\n}\n"), rule: RuleExprReferenceDenied},
	{name: "dynamic label terraform", level: "L3", files: l3sub(l3Resource, "resource \"aws_rds_cluster\" \"this\" {\n  dynamic \"terraform\" {\n    for_each = [1]\n    content {}\n  }\n}\n"), rule: RuleExprReferenceDenied},
	{name: "iterator out of scope", files: l1("locals {\n  a = [for x in [] : x]\n  b = x\n}\n"), rule: RuleExprReferenceDenied},

	{name: "heredoc interpolation", files: l1("locals {\n  a = <<EOT\n${local.b}\nEOT\n  b = 1\n}\n"), rule: RuleExprHeredocTemplate},
	{name: "heredoc directive", files: l1("locals {\n  a = <<-EOT\n  %{if true}x%{endif}\n  EOT\n}\n"), rule: RuleExprHeredocTemplate},
	{name: "test file in L1", files: map[string]string{"main.tf": okTF, "main.tftest.hcl": l2Run}, rule: RuleFileNotTF},
	{name: "test file in L3", level: "L3", files: map[string]string{"main.tf": l3TF, "main.tftest.hcl": l2Run}, rule: RuleFileNotTF},
}

// l2Main and l2Test are a valid L2 tree to add one violation to: a pinned
// provider, mocked, and a plan-only run.
const (
	l2Main = l3Terraform + `resource "aws_s3_bucket" "this" {
  bucket = "fixture"
}
`
	l2Mock = "mock_provider \"aws\" {}\n"
	l2Run  = "run \"plan\" {\n  command = plan\n}\n"
	l2Test = l2Mock + l2Run
)

// l2 is the L2 tree with main and test appended to its two files.
func l2(main, test string) map[string]string {
	return map[string]string{"main.tf": l2Main + main, "main.tftest.hcl": l2Test + test}
}

// l2sub is the L2 tree with old replaced by repl in the file that holds it.
func l2sub(old, repl string) map[string]string {
	files := l2("", "")
	for name, src := range files {
		if strings.Contains(src, old) {
			files[name] = strings.Replace(src, old, repl, 1)
			return files
		}
	}
	panic(old)
}

// l2run is the L2 tree with body added to its run block.
func l2run(body string) map[string]string {
	return l2sub("  command = plan\n", "  command = plan\n"+body)
}

var l2Cases = []ruleCase{
	{name: "no test file", files: l1(okTF), rule: RuleTestFileCount},
	{name: "two test files", files: map[string]string{"main.tf": okTF, "a.tftest.hcl": l2Run, "b.tftest.hcl": l2Run}, rule: RuleTestFileCount},
	{name: "test json", files: map[string]string{"main.tf": l2Main, "main.tftest.hcl": l2Test, "x.tftest.json": "{}"}, rule: RuleFileNotTF},
	{name: "mock file", files: map[string]string{"main.tf": l2Main, "main.tftest.hcl": l2Test, "aws.tfmock.hcl": ""}, rule: RuleFileNotTF},
	{name: "tests dir", files: l2("", ""), setup: func(t *testing.T, dir string) {
		mustDo(t, os.Mkdir(filepath.Join(dir, "tests"), 0o755))
	}, rule: RuleFileSubdirectory},
	{name: "test parse error", files: map[string]string{"main.tf": okTF, "main.tftest.hcl": "run {\n"}, rule: RuleHCLParseError},
	{name: "test heredoc template", files: l2run("  assert {\n    condition     = true\n    error_message = <<EOT\n${var.b}\nEOT\n  }\n"), rule: RuleExprHeredocTemplate},

	{name: "provider block", files: l2("provider \"aws\" {\n  region = \"us-east-1\"\n}\n", ""), rule: RuleProviderDenied},
	{name: "terraform provider block", files: l2("provider \"terraform\" {}\n", ""), rule: RuleProviderDenied},
	{name: "test provider block", files: l2("", "provider \"aws\" {}\n"), rule: RuleProviderDenied},
	{name: "provider alias reference", files: l2sub("  bucket = \"fixture\"\n", "  provider = aws.x\n"), rule: RuleProviderDenied},
	{name: "not mocked", files: l2sub(l2Mock, ""), rule: RuleTestProviderNotMocked},
	{name: "declared not mocked", files: map[string]string{"main.tf": l3Terraform, "main.tftest.hcl": l2Run}, rule: RuleTestProviderNotMocked},
	{name: "undeclared provider", files: l2("resource \"google_x\" \"y\" {}\n", "mock_provider \"google\" {}\n"), rule: RuleProviderNotPinned},
	{name: "undeclared provider data", files: l2("data \"google_x\" \"y\" {}\n", "mock_provider \"google\" {}\n"), rule: RuleProviderNotPinned},
	{name: "meta provider undeclared", files: l2sub("  bucket = \"fixture\"\n", "  provider = google\n"), rule: RuleProviderNotPinned},
	{name: "version unpinned", files: l2sub(`"6.15.0"`, `"~> 6.0"`), rule: RuleProviderNotPinned},
	{name: "remote state", files: l2("data \"terraform_remote_state\" \"s\" {}\n", ""), rule: RuleResourceTypeDenied},
	{name: "builtin resource", files: l2("resource \"terraform_other\" \"s\" {}\n", ""), rule: RuleResourceTypeDenied},

	{name: "module", files: l2("module \"m\" {\n  source = \"./m\"\n}\n", ""), rule: RuleBlockDenied},
	{name: "import", files: l2("import {\n  to = aws_s3_bucket.this\n  id = \"x\"\n}\n", ""), rule: RuleBlockDenied},
	{name: "removed", files: l2("removed {\n  from = aws_s3_bucket.old\n}\n", ""), rule: RuleBlockDenied},
	{name: "check", files: l2("check \"c\" {}\n", ""), rule: RuleBlockDenied},
	{name: "ephemeral", files: l2("ephemeral \"aws_x\" \"e\" {}\n", ""), rule: RuleBlockDenied},
	{name: "action", files: l2("action \"aws_lambda_invoke\" \"a\" {}\n", ""), rule: RuleBlockDenied},
	{name: "backend", files: l2("terraform {\n  backend \"local\" {}\n}\n", ""), rule: RuleBlockDenied},
	{name: "provisioner", files: l2sub("  bucket = \"fixture\"\n", "  provisioner \"local-exec\" {\n    command = \"id\"\n  }\n"), rule: RuleBlockDenied},
	{name: "data provisioner", files: l2("data \"aws_x\" \"y\" {\n  connection {}\n}\n", ""), rule: RuleBlockDenied},
	{name: "data file argument", files: l2("data \"aws_x\" \"y\" {\n  filename = \"/etc/passwd\"\n}\n", ""), rule: RuleBlockAttributeDenied},
	{name: "data function", files: l2("data \"aws_x\" \"y\" {\n  id = file(\"x\")\n}\n", ""), rule: RuleExprFunctionDenied},
	{name: "path reference", files: l2("locals {\n  a = path.module\n}\n", ""), rule: RuleExprReferenceDenied},
	{name: "for binds data", files: l2("locals {\n  a = [for data in [1] : data]\n}\n", ""), rule: RuleExprReferenceDenied},
	{name: "moved module", files: l2("moved {\n  from = module.a.aws_s3_bucket.this\n  to   = aws_s3_bucket.this\n}\n", ""), rule: RuleExprReferenceDenied},
	{name: "moved data", files: l2("moved {\n  from = data.aws_x.a\n  to   = aws_s3_bucket.this\n}\n", ""), rule: RuleExprReferenceDenied},
	{name: "moved expression", files: l2("moved {\n  from = \"aws_s3_bucket.a\"\n  to   = aws_s3_bucket.this\n}\n", ""), rule: RuleBlockAttributeDenied},
	{name: "moved argument", files: l2("moved {\n  from = aws_s3_bucket.a\n  to   = aws_s3_bucket.this\n  x    = 1\n}\n", ""), rule: RuleBlockAttributeDenied},
	{name: "moved missing to", files: l2("moved {\n  from = aws_s3_bucket.a\n}\n", ""), rule: RuleBlockAttributeDenied},

	{name: "no command", files: l2sub("  command = plan\n", ""), rule: RuleTestCommandNotPlan},
	{name: "apply", files: l2sub("command = plan", "command = apply"), rule: RuleTestCommandNotPlan},
	{name: "quoted plan", files: l2sub("command = plan", `command = "plan"`), rule: RuleTestCommandNotPlan},
	{name: "second run applies", files: l2("", "run \"apply\" {}\n"), rule: RuleTestCommandNotPlan},
	{name: "refresh false", files: l2run("  plan_options {\n    refresh = false\n  }\n"), rule: RuleTestPlanOptionsDenied},
	{name: "refresh-only", files: l2run("  plan_options {\n    mode = refresh-only\n  }\n"), rule: RuleTestPlanOptionsDenied},
	{name: "target", files: l2run("  plan_options {\n    target = [aws_s3_bucket.this]\n  }\n"), rule: RuleTestPlanOptionsDenied},
	{name: "run module", files: l2run("  module {\n    source = \"./other\"\n  }\n"), rule: RuleBlockDenied},
	{name: "override module", files: l2("", "override_module {\n  target = module.m\n}\n"), rule: RuleBlockDenied},
	{name: "run providers", files: l2run("  providers = {\n    aws = aws\n  }\n"), rule: RuleBlockAttributeDenied},
	{name: "run state_key", files: l2run("  state_key = \"x\"\n"), rule: RuleBlockAttributeDenied},
	{name: "test block", files: l2("", "test {\n  parallel = true\n}\n"), rule: RuleBlockDenied},
	{name: "test attribute", files: l2("", "a = 1\n"), rule: RuleBlockDenied},
	{name: "expect failures module", files: l2run("  expect_failures = [module.m]\n"), rule: RuleExprReferenceDenied},
	{name: "expect failures check", files: l2run("  expect_failures = [check.c]\n"), rule: RuleExprReferenceDenied},
	{name: "expect failures call", files: l2run("  expect_failures = [file(\"x\")]\n"), rule: RuleBlockAttributeDenied},
	{name: "assert function", files: l2run("  assert {\n    condition     = file(\"/etc/passwd\") == \"\"\n    error_message = \"x\"\n  }\n"), rule: RuleExprFunctionDenied},
	{name: "assert path", files: l2run("  assert {\n    condition     = path.cwd == \"\"\n    error_message = \"x\"\n  }\n"), rule: RuleExprReferenceDenied},
	{name: "assert argument", files: l2run("  assert {\n    condition     = true\n    error_message = \"x\"\n    x             = 1\n  }\n"), rule: RuleBlockAttributeDenied},

	{name: "mock source", files: l2sub(l2Mock, "mock_provider \"aws\" {\n  source = \"./mocks\"\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "mock alias", files: l2("", "mock_provider \"aws\" {\n  alias = \"x\"\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "mock nested", files: l2sub(l2Mock, "mock_provider \"aws\" {\n  mock_resource \"aws_s3_bucket\" {\n    defaults = {}\n    x {}\n  }\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "mock override_during apply", files: l2sub(l2Mock, "mock_provider \"aws\" {\n  mock_resource \"aws_s3_bucket\" {\n    override_during = apply\n  }\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "mock override_during string", files: l2sub(l2Mock, "mock_provider \"aws\" {\n  override_during = \"plan\"\n}\n"), rule: RuleBlockAttributeDenied},
	{name: "mock defaults function", files: l2sub(l2Mock, "mock_provider \"aws\" {\n  mock_data \"aws_x\" {\n    defaults = {\n      a = file(\"/etc/passwd\")\n    }\n  }\n}\n"), rule: RuleTestValueNotLiteral},
	{name: "mock defaults reference", files: l2sub(l2Mock, "mock_provider \"aws\" {\n  mock_resource \"aws_s3_bucket\" {\n    defaults = {\n      a = var.x\n    }\n  }\n}\n"), rule: RuleTestValueNotLiteral},
	{name: "mock defaults template", files: l2sub(l2Mock, "mock_provider \"aws\" {\n  mock_resource \"aws_s3_bucket\" {\n    defaults = {\n      a = \"${1}\"\n    }\n  }\n}\n"), rule: RuleTestValueNotLiteral},
	{name: "mock defaults key expression", files: l2sub(l2Mock, "mock_provider \"aws\" {\n  mock_resource \"aws_s3_bucket\" {\n    defaults = {\n      (var.k) = 1\n    }\n  }\n}\n"), rule: RuleTestValueNotLiteral},
	{name: "variables reference", files: l2("", "variables {\n  a = run.plan.x\n}\n"), rule: RuleTestValueNotLiteral},
	{name: "variables conditional", files: l2run("  variables {\n    a = true ? 1 : 2\n  }\n"), rule: RuleTestValueNotLiteral},
	{name: "variables negate reference", files: l2run("  variables {\n    a = -var.b\n  }\n"), rule: RuleTestValueNotLiteral},
	{name: "override values", files: l2("", "override_resource {\n  target = aws_s3_bucket.this\n  values = {\n    id = uuid()\n  }\n}\n"), rule: RuleTestValueNotLiteral},
	{name: "override target module", files: l2run("  override_resource {\n    target = module.m.aws_s3_bucket.this\n  }\n"), rule: RuleExprReferenceDenied},
	{name: "override target expression", files: l2run("  override_data {\n    target = \"data.aws_x.y\"\n  }\n"), rule: RuleBlockAttributeDenied},
	{name: "override_during", files: l2("", "override_resource {\n  target          = aws_s3_bucket.this\n  override_during = apply\n}\n"), rule: RuleBlockAttributeDenied},
}

// Each L2 case breaks exactly one rule and is reported with that rule only.
func TestCheckRulesL2(t *testing.T) {
	t.Parallel()
	for _, tc := range l2Cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, data := range tc.files {
				mustDo(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644))
			}
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			r, err := Check(Options{Dir: dir, Level: "L2", Pins: testPins})
			if err != nil {
				t.Fatal(err)
			}
			if r.OK || len(r.Violations) == 0 {
				t.Fatalf("passes: %+v", r)
			}
			for _, v := range r.Violations {
				if v.Rule != tc.rule {
					t.Errorf("violation %+v, want rule %s only", v, tc.rule)
				}
			}
		})
	}
}

// The base L2 tree and its literal and in-tree variants pass.
func TestCheckPassesL2(t *testing.T) {
	t.Parallel()
	for name, files := range map[string]map[string]string{
		"base":         l2("", ""),
		"builtin only": {"main.tf": okTF, "ladder.tftest.hcl": l2Run},
		"literals": l2run("  variables {\n    a = -1\n    b = [\"x\", null, true, 1.5]\n    c = {\n      \"k\" = { n = -2 }\n    }\n    d = <<EOT\nplain\nEOT\n  }\n" +
			"  expect_failures = [var.a, output.o, aws_s3_bucket.this, data.aws_x.y]\n"),
		"override":             l2("", "override_resource {\n  target = aws_s3_bucket.this[\"k\"]\n  values = {}\n}\noverride_data {\n  target = data.aws_x.y\n  values = { id = \"x\" }\n}\n"),
		"override_during plan": l2sub(l2Mock, "mock_provider \"aws\" {\n  override_during = plan\n  mock_resource \"aws_s3_bucket\" {\n    override_during = plan\n    defaults        = { arn = \"x\" }\n  }\n  override_data {\n    target          = data.aws_x.y\n    override_during = plan\n  }\n}\n"),
		"meta provider":        l2sub("  bucket = \"fixture\"\n", "  provider = aws\n"),
		"assert scope":         l2run("  assert {\n    condition     = var.a == local.b && output.o == run.plan.x && data.aws_x.y.id == aws_s3_bucket.this.id\n    error_message = \"${var.a} ok\"\n  }\n"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for n, data := range files {
				mustDo(t, os.WriteFile(filepath.Join(dir, n), []byte(data), 0o644))
			}
			r, err := Check(Options{Dir: dir, Level: "L2", Pins: testPins})
			if err != nil {
				t.Fatal(err)
			}
			if !r.OK {
				t.Fatalf("result %+v", r)
			}
		})
	}
}

func manyFiles(n int) map[string]string {
	m := map[string]string{}
	for i := range n {
		m[fmt.Sprintf("f%03d.tf", i)] = "locals {}\n"
	}
	return m
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Each case breaks exactly one rule and is reported with that rule only.
func TestCheckRules(t *testing.T) {
	t.Parallel()
	for _, tc := range ruleCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, data := range tc.files {
				mustDo(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644))
			}
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			o := Options{Dir: dir, Level: tc.level, Pins: testPins}
			if tc.level == "L3" {
				o.Resource = "aws_rds_cluster"
			}
			r, err := Check(o)
			if err != nil {
				t.Fatal(err)
			}
			if r.OK || len(r.Violations) == 0 {
				t.Fatalf("passes: %+v", r)
			}
			for _, v := range r.Violations {
				if v.Rule != tc.rule {
					t.Errorf("violation %+v, want rule %s only", v, tc.rule)
				}
				if v.File == "" || v.Line < 0 {
					t.Errorf("violation without a place: %+v", v)
				}
			}
		})
	}
}

// Every rule has a negative case.
func TestEveryRuleTested(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, tc := range ruleCases {
		seen[tc.rule] = true
	}
	for _, tc := range expectCases {
		seen[tc.rule] = true
	}
	for _, tc := range l2Cases {
		seen[tc.rule] = true
	}
	for _, r := range Rules {
		if !seen[r] {
			t.Errorf("rule %s has no case", r)
		}
	}
}

func TestViolationPlaceAndCap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := "locals {\n" + strings.Repeat("  a = file(\"x\")\n", 1) + "}\n"
	for range MaxViolations {
		src += "locals {\n  b = timestamp()\n}\n"
	}
	mustDo(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o644))
	r, err := Check(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Violations) != MaxViolations+1 {
		t.Fatalf("%d violations", len(r.Violations))
	}
	if v := r.Violations[0]; v.File != "main.tf" || v.Line != 2 || v.Rule != RuleExprFunctionDenied || v.Detail != "file" {
		t.Errorf("first %+v", v)
	}
	if v := r.Violations[MaxViolations]; v.Rule != RuleLimitViolations || v.File != "." {
		t.Errorf("last %+v", v)
	}
}

func TestCheckErrors(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "f")
	mustDo(t, os.WriteFile(file, nil, 0o644))
	link := filepath.Join(t.TempDir(), "l")
	mustDo(t, os.Symlink(filepath.Join(td, "l1"), link))
	for name, o := range map[string]Options{
		"missing":            {Dir: filepath.Join(td, "none")},
		"file":               {Dir: file},
		"link":               {Dir: link},
		"level":              {Dir: filepath.Join(td, "l1"), Level: "L4"},
		"L2 resource":        {Dir: filepath.Join(td, "l2"), Level: "L2", Resource: "aws_rds_cluster"},
		"L3 no resource":     {Dir: filepath.Join(td, "l3"), Level: "L3"},
		"L1 resource":        {Dir: filepath.Join(td, "l1"), Resource: "aws_rds_cluster"},
		"terraform_data":     {Dir: filepath.Join(td, "l3"), Level: "L3", Resource: "terraform_data"},
		"provider unknown":   {Dir: filepath.Join(td, "l3"), Level: "L3", Resource: "google_sql_database"},
		"resource malformed": {Dir: filepath.Join(td, "l3"), Level: "L3", Resource: "aws-rds"},
		"not curated":        {Dir: filepath.Join(td, "l3"), Level: "L3", Resource: "aws_lambda_function"},
	} {
		if _, err := Check(o); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestReadPins(t *testing.T) {
	t.Parallel()
	pins, err := ReadPins(filepath.Join(td, "pins.json"))
	if err != nil || !slices.Equal(pins, testPins) {
		t.Fatalf("%v %v", pins, err)
	}
	dir := t.TempDir()
	for name, data := range map[string]string{
		"syntax":      `{"providers":[`,
		"unknown key": `{"providers":[],"x":1}`,
		"no source":   `{"providers":[{"version":"6.15.0"}]}`,
		"constraint":  `{"providers":[{"source":"hashicorp/aws","version":">= 6.0"}]}`,
		"bad source":  `{"providers":[{"source":"aws","version":"6.15.0"}]}`,
		"bad sha256":  `{"providers":[{"source":"hashicorp/aws","version":"6.15.0","sha256":"x"}]}`,
		"trailing":    `{"providers":[]} {}`,
		"duplicate":   `{"providers":[{"source":"hashicorp/aws","version":"6.15.0"},{"source":"hashicorp/aws","version":"6.16.0"}]}`,
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		mustDo(t, os.WriteFile(p, []byte(data), 0o644))
		if _, err := ReadPins(p); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	p := filepath.Join(dir, "sha.json")
	mustDo(t, os.WriteFile(p, []byte(`{"providers":[{"source":"registry.terraform.io/hashicorp/aws","version":"6.15.0","sha256":"`+strings.Repeat("a", 64)+`"}]}`), 0o644))
	if _, err := ReadPins(p); err != nil {
		t.Errorf("pin with sha256: %v", err)
	}
}

// The function allowlist is sorted and holds none of the functions that read
// files, the clock, randomness or the environment.
func TestPureFunctions(t *testing.T) {
	t.Parallel()
	if !slices.IsSorted(pureFunctions) || len(slices.Compact(slices.Clone(pureFunctions))) != len(pureFunctions) {
		t.Error("pureFunctions is not sorted and unique")
	}
	for _, f := range []string{"file", "filebase64", "filebase64sha256", "filebase64sha512", "fileexists", "filemd5",
		"filesha1", "filesha256", "filesha512", "fileset", "templatefile", "templatestring", "abspath", "pathexpand",
		"dirname", "basename", "timestamp", "plantimestamp", "uuid", "uuidv5", "bcrypt", "rsadecrypt", "ephemeralasnull",
		"yamldecode"} {
		if slices.Contains(pureFunctions, f) {
			t.Errorf("%s is on the allowlist", f)
		}
	}
}

// The schema's rule enum is the rule list.
func TestSchemaRules(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../schema/fixture-check.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Defs struct {
			Violation struct {
				Properties struct {
					Rule struct {
						Enum []string `json:"enum"`
					} `json:"rule"`
				} `json:"properties"`
			} `json:"violation"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	want := append(slices.Clone(Rules), RuleLimitViolations)
	if got := s.Defs.Violation.Properties.Rule.Enum; !slices.Equal(got, want) {
		t.Errorf("schema rules %v, want %v", got, want)
	}
}
