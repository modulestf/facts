package main

const (
	preCommitShort = "Read what pre-commit-terraform's hooks write and pin"
	preCommitLong  = `Read the files pre-commit-terraform's hooks write and the pins that choose
what they write, as data: it runs neither pre-commit nor git. The host
extracts each input from the revision it reads into a file of its own; a path
with nothing at it is a file absent at that revision. Every reader fails
closed: input it might read another way than pre-commit, a YAML loader or the
hook gives unknown or custom, never a match.`

	preCommitDeriveShort = "Print the wrapper versions.tf the wrapper hook derives"
	preCommitDeriveLong  = `Print the bytes terraform_wrapper_module_for_each writes to a wrapper
versions.tf, from v1.106.0 on, for a module's versions.tf: the file with
user_agent and module_name removed from each provider_meta block.

derive is undefined for a file over 64 KiB, with no final newline, a byte
outside printable ASCII, tab and newline, or a heredoc marker anywhere, and
for a provider_meta block that holds anything but comments, blank lines and
single-line attributes, the two removed attributes also as a bracketed list
over several lines. A file it cannot read has no derive either.

Exit codes:
  0  the derived bytes
  2  an error: bad input or the timeout
  3  derive is undefined; nothing is printed`
	preCommitDeriveExample = `  mtf-facts pre-commit derive versions.tf > wrappers/versions.tf.derived`

	preCommitWrapperShort = "Classify a wrapper versions.tf against its module's"
	preCommitWrapperLong  = `Print one line for a wrapper versions.tf and its module's versions.tf, as
the wrapper hook pinned in the .pre-commit-config.yaml would write it:

  derived    the pin is v1.106.0 or later and the wrapper is derive of the
             module (see pre-commit derive)
  copied     the pin is from v1.89.0 and the wrapper is the module file
  differs    the wrapper is not what the hook writes at the pin
  unknown <reason>

The reasons, in the order they are decided: wrapper or module followed by
absent, link, not a regular file, over size (64 KiB) or read failed; no pin
(no configuration, or no pre-commit-terraform entry); pin unreadable (YAML
this reader does not follow or that a YAML loader could read another pin
from, a second entry or mention of the repository or the hook, a rev that is
no plain tag); hook options (an option that moves the hook to another
provider); hook not listed; pin not a release tag; old hook (before
v1.89.0, when the hook wrote a fixed template); provider_meta (at a derive
pin, a module with a provider_meta block other than aws); derive undefined.

Exit codes:
  0  the line
  2  an error: bad input or the timeout`
	preCommitWrapperExample = `  mtf-facts pre-commit wrapper --config .pre-commit-config.yaml \
    --module versions.tf --wrapper wrappers/versions.tf`

	preCommitDocsShort = "Read the terraform_docs hook and the workflow's terraform-docs pin"
	preCommitDocsLong  = `Print the terraform_docs hook of the .pre-commit-config.yaml, and with
--workflow the terraform-docs version a workflow pins against --version:

  hook profile       the one hook of pre-commit-terraform with that id, whose
                     only setting is args: the single --args=--lockfile=false
  hook custom        anything else, an absent or unreadable file included
  workflow same      every TERRAFORM_DOCS_VERSION or terraform-docs-version
                     key pins --version
  workflow mismatch  one pins another version
  workflow none      no pin, or no workflow file
  workflow unreadable  a pin that is no plain version, or a file it cannot read

Each file is read up to 1 MiB.

Exit codes:
  0  the lines
  2  an error: bad input or the timeout`
	preCommitDocsExample = `  mtf-facts pre-commit docs --config .pre-commit-config.yaml \
    --workflow .github/workflows/pre-commit.yml --version v0.24.0`

	preCommitConfigUsage   = ".pre-commit-config.yaml `file`"
	preCommitModuleUsage   = "the module's versions.tf `file`"
	preCommitWrapperUsage  = "the wrapper versions.tf `file`"
	preCommitWorkflowUsage = "workflow `file` that pins terraform-docs"
	preCommitVersionUsage  = "terraform-docs `version` the workflow pin is compared with, vX.Y.Z"
	preCommitDocsPair      = "--workflow and --version go together"
	preCommitDocsVersion   = "--version must be vX.Y.Z"
)
