package main

const (
	planGatesShort = "Run the plan pass gates over .tf files before Terraform runs"
	planGatesLong  = `Run the plan pass's gates G0 to G3 over the .tf files listed on standard
input, one path a line, before any Terraform command runs on them with
credentials, and print one line per hit: "<gate> <path>:<line>", line 0 for
the whole file. The line UNREADABLE stands for a file the caller could not
list, and is a G0 hit.

G0 is a file the gates cannot read as text: unreadable or over 8 MiB, an
override file, a carriage return, form feed or vertical tab, a byte over
0x7f in code or a label, a block comment, or in runner mode a string,
comment or heredoc that does not end. G1 is a required provider outside the
allowlist, G2 a module source outside the clone or the terraform-aws-modules
registry namespace (a source that holds a NUL byte is a G2 hit; the Perl
gates truncated it at the NUL; a source Terraform reads otherwise than the
text, as behind a commented-out source, is one too), G3 a block or
expression that runs code or reads an account. Runner mode relaxes the
code-execution gates and records the exceptions Xrole, a fixed role in
another account, and Xdocker, a Docker daemon.

The rules read text as the plan pass always has, comments and strings
included, so the lines are those the pass printed before, in its order,
with duplicates, followed in each file by any G2 lines the second read of
module sources adds. Only the listed files are read, and the names in their
directories; links are followed.

Exit 0 with no hit, 1 with hits, 2 on a usage error, bad input, the output
cap or the timeout. A caller must stop on 2.`
	planGatesExample = `  scope | mtf-facts plan-gates --scope clone --mode laptop \
    --root "$CLONE" --example "$CLONE/examples/complete"
  find "$MODULE_DIR" -name '*.tf' | mtf-facts plan-gates --scope downloaded \
    --mode runner --root "$CLONE" --provider compliancetf/*`
	planGatesScopeUsage    = "what the files are: clone, the clone's own files, or downloaded, a downloaded module's"
	planGatesModeUsage     = "laptop, the strict gates, or runner, the gates of a runner container"
	planGatesRootUsage     = "the clone `dir`, which local module sources must stay in"
	planGatesExampleUsage  = "the example root `dir` the laptop event bus and data source region rules read"
	planGatesProviderUsage = "in runner mode, one more allowed provider, `ns/name` or ns/*; repeat it"
)
