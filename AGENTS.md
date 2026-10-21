# AGENTS.md

Guidance for coding agents working in this repository.

`mtf-facts` is an offline Go CLI that parses untrusted Terraform module trees. Treat every input tree as hostile: no network, no program execution, bounded reads, and a limit on everything an input can grow.

## Go development

Before any Go coding, review, debugging, troubleshooting, or setup task, load the `samber/cc-skills-golang@golang-how-to` skill first - it routes to whichever other Go skills the task needs.

## Required Go skills

The following Go skills from `samber/cc-skills-golang` MUST always be applied when working on this project. Load them at the start of every Go-related task, regardless of whether the user explicitly mentions them.

- `samber/cc-skills-golang@golang-code-style`
- `samber/cc-skills-golang@golang-data-structures`
- `samber/cc-skills-golang@golang-design-patterns`
- `samber/cc-skills-golang@golang-documentation`
- `samber/cc-skills-golang@golang-error-handling`
- `samber/cc-skills-golang@golang-modernize`
- `samber/cc-skills-golang@golang-naming`
- `samber/cc-skills-golang@golang-safety`
- `samber/cc-skills-golang@golang-security`
- `samber/cc-skills-golang@golang-testing`
- `samber/cc-skills-golang@golang-troubleshooting`
- `samber/cc-skills-golang@golang-cli`
- `samber/cc-skills-golang@golang-continuous-integration`
- `samber/cc-skills-golang@golang-dependency-management`

## Checks

Run all of these before you hand off a change:

```sh
go vet ./...
go test -race -count=1 ./...
golangci-lint run
govulncheck ./...
```

## Writing style

Plain ASCII in code, comments, docs and commit messages. No vendor or model attribution anywhere.
