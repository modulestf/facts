# pre-commit fixtures

Cases for `mtf-facts pre-commit derive`, `wrapper` and `docs`. Each expected result was produced by the shell and Perl readers these commands replace, run on the same files, and the tests in `cmd/mtf-facts/precommit_test.go` and `internal/facts/precommit_test.go` require the same bytes and exit codes.

| Path | What it is |
|------|------------|
| `recorded/<repository>/` | A module's `versions.tf` and its `wrappers/versions.tf`, recorded from three terraform-aws-modules repositories (Apache License 2.0). Each wrapper is the module file as the wrapper hook writes it. |
| `derive/<case>/in.tf` | Input of `derive`. `want.tf` holds the derived bytes; a case with no `want.tf` is undefined (exit 3, no output). |
| `wrapper/<case>/` | `module.tf`, `wrapper.tf` and `config.yaml` (absent in `no-config`). `want` is the line `wrapper` prints; `pin` is the pin the configuration alone gives. |
| `docs/<case>/` | `config.yaml` and `workflow.yml`, each absent in some cases, read with `--version v0.24.0`. `want` is what `docs` prints. |

The configuration files are stored without their leading dot, so no tool acts on them here.

Cases a committed file cannot hold, such as a link, a directory, a file over a size bound or a missing wrapper, are built in a temporary directory by `TestPreCommitFileStates`.

`.gitattributes` turns off end-of-line conversion here, so the CR bytes of the CRLF cases reach the tests as written.
