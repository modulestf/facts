# Profile: terraform-aws-modules (test copy)

A copy of the profile constants of the terraform-aws-modules profile in the modulestf
maintainer skill, for the tests of this repository. The blocks are byte for byte the
blocks of that profile. The rule text they restate stays in that profile. The
files under templates/ must equal that profile's templates byte for byte, so a
template-drift rule tested here judges the real upstream bytes. Update them together.

Typography exception: `github-workflows-lock.yml.template` U+23F3

```profile-constants
name: required-files
root: main.tf variables.tf outputs.tf versions.tf README.md LICENSE
submodule: main.tf variables.tf outputs.tf versions.tf README.md
example: main.tf outputs.tf versions.tf README.md
example-dirs: examples/basic examples/complete
```

```profile-constants
name: regions
primary: eu-west-1
secondary: us-east-1
```

```profile-constants
name: argument-order
first: count for_each
last: tags labels
```

```profile-constants
name: template-destinations
.editorconfig.template: .editorconfig
.gitignore.template: .gitignore
.releaserc.json.template: .releaserc.json
github-workflows-pre-commit.yml.template: .github/workflows/pre-commit.yml
github-workflows-release.yml.template: .github/workflows/release.yml
github-workflows-lock.yml.template: .github/workflows/lock.yml
github-workflows-pr-title.yml.template: .github/workflows/pr-title.yml
github-workflows-stale-actions.yaml.template: .github/workflows/stale-actions.yaml
.pre-commit-config.yaml.template: .pre-commit-config.yaml
```

```profile-constants
name: profile-owned-files
verbatim: .editorconfig .gitignore .releaserc.json .github/workflows/pre-commit.yml .github/workflows/release.yml .github/workflows/lock.yml .github/workflows/pr-title.yml .github/workflows/stale-actions.yaml
workflow-dir: .github/workflows
```

```profile-constants
name: tooling
files: .checkov.yaml .checkov.yml trivy.yaml .trivyignore kics.config infracost.yml
dirs: .tfsec
```

```profile-constants
name: banner
line: [![SWUbanner](https://raw.githubusercontent.com/vshymanskyy/StandWithUkraine/main/banner2-direct.svg)](https://github.com/vshymanskyy/StandWithUkraine/blob/main/docs/README.md)
```

```profile-constants
name: adjunct-types
types: aws_iam_role aws_iam_role_policy aws_iam_role_policy_attachment aws_iam_policy aws_iam_instance_profile aws_cloudwatch_log_group aws_cloudwatch_log_resource_policy aws_security_group aws_vpc_security_group_ingress_rule aws_vpc_security_group_egress_rule aws_security_group_rule
```

