package facts

import "testing"

func TestProviderKey(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"aws":                                 "registry.terraform.io/hashicorp/aws",
		"Acme/Example":                        "registry.terraform.io/acme/example",
		"example.com/acme/example":            "example.com/acme/example",
		"registry.terraform.io/hashicorp/aws": "registry.terraform.io/hashicorp/aws",
	} {
		if got := ProviderKey(in); got != want {
			t.Errorf("ProviderKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResourceProviderSource(t *testing.T) {
	t.Parallel()
	m := parseOne(t, `terraform {
  required_providers {
    aws    = { source = "hashicorp/aws" }
    awsalt = { source = "Acme/AWS-Fork" }
    legacy = ">= 1.0"
  }
}
resource "aws_a" "plain" {}
resource "aws_b" "alias" { provider = awsalt.east }
resource "aws_c" "same" { provider = aws.west }
resource "legacy_d" "x" {}
data "undeclared_e" "x" {}
`)
	want := map[string]string{
		"aws_a":        "registry.terraform.io/hashicorp/aws",
		"aws_b":        "registry.terraform.io/acme/aws-fork", // local name and source differ from the prefix
		"aws_c":        "registry.terraform.io/hashicorp/aws",
		"legacy_d":     "registry.terraform.io/hashicorp/legacy", // no source: implied namespace
		"undeclared_e": "registry.terraform.io/hashicorp/undeclared",
	}
	if len(m.Resources) != len(want) {
		t.Fatalf("%d resources", len(m.Resources))
	}
	for _, r := range m.Resources {
		if got := ResourceProviderSource(m, r); got != want[r.Type] {
			t.Errorf("%s: got %q, want %q", r.Type, got, want[r.Type])
		}
	}
}

func TestIsTypeLabel(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]bool{
		"aws_s3_bucket": true, "_x": true, "a-b9": true, "A": true,
		"": false, "9a": false, "-a": false, "a b": false, "a\nb": false, "a\rb": false, "a.b": false, "\u00e9": false,
	} {
		if got := IsTypeLabel(s); got != want {
			t.Errorf("IsTypeLabel(%q) = %v, want %v", s, got, want)
		}
	}
}
