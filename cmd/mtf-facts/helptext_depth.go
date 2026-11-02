package main

// Help text of depth.

const (
	depthShort = "Decide how deep a review of a change must go: light, focused or deep"
	depthLong  = `Decide the review depth of the change from the merge base tree (--base) to
the head tree (--head), from both trees, the pull request files list
(--files, GitHub's pulls/<n>/files shape), and the behaviour --json and
affected --json reports of the same change. It never runs Terraform.

Both trees are parsed, HCL and JSON forms, and compared block by block. Every
changed value (resource and data arguments, locals, variables, outputs,
module call arguments, variables file assignments, and an argument whose file
sink string resolves to a changed file) is followed to every consumer,
through outputs and module calls, into every argument and meta-argument. A
changed argument of a managed resource affects references to that attribute
and to the whole resource; one of a data source affects every reference.
Every string a file sink reads (a file function's path, a provisioner
argument, an argument named like a path) must resolve to literals joined to
path.module or path.root.

light: only Markdown, CHANGELOG, docs/, LICENSE, .gitignore or .editorconfig
paths changed, no Terraform-read file, every file sink string resolves and
reaches no changed path, the head calls only built-in functions, and the
classifier reports the change inert.
focused: a Terraform change within 6 Terraform files, 200 changed lines, 4
changed resource, data and module blocks and 12 selected examples, with no
deep class and every classifier category on the allow-list.
deep: anything else. Anything not proven, such as a missing classifier
report, a file that does not parse or a consumer that is not modeled, is deep.

It prints one JSON object: the depth, its reasons (fixed tokens), the checks
and examples out of scope, the touched types, the walk coverage, the limit
counts and the schema-pass tokens. Exit 0 with the report, 2 on a missing
tree, a files list that does not read, a limit or the timeout.`
	depthExample = `  mtf-facts depth --head ./head --base ./base --files files.json \
    --classify behaviour.json --affected affected.json > depth.json`
	depthHeadUsage     = "the head `tree`"
	depthBaseUsage     = "the merge base `tree`"
	depthFilesUsage    = "the pull request files list `file`"
	depthClassifyUsage = "behaviour --json report `file` of the same change; without it the change is deep"
	depthAffectedUsage = "affected --json report `file` of the same change; without it the change is deep"
	depthExamplesUsage = "comma-separated example `dirs` the host knows of, selected unless proven out of scope"
	depthRequired      = "--head, --base and --files are required"
)
