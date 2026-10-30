package main

// Help text of behaviour and claims-eval.

const (
	jsonUsage    = "write the report as JSON, the only format"
	jsonRequired = "--json is required: JSON is the only output format"
)

const (
	behaviourShort = "Classify a change into triggers and the verification levels each needs"
	behaviourLong  = `Classify the change from a base tree to the module tree in <dir>. Give the
base tree with --base, or the change with --diff, a GitHub pull request files
list or a unified diff the base tree is rebuilt from; the report is the same
for every diff context size.

Every changed tree entry is listed. A documentation or metadata path is inert
when no read site in either revision's configuration can reach it and every
.tf file parsed; a .tf file is inert when its change is only comments, layout
or blocks moved between files of one directory. Every other change is
decomposed into triggers, one per AST-level change with one category, and
each trigger selects its levels: L1 offline expression evaluation, L3 curated
provider fixtures, L4 the example plans, or compat for a version floor. A
changed resource argument also yields an unknown-propagation-candidate
trigger when the provider schema given with --schema marks it Optional and
Computed, or when there is no schema, or it lacks the attribute.

At most 40 triggers are listed; capped counts the rest. It never runs
Terraform. Exit 0 with the report, 2 on bad input, a limit or the timeout.`
	behaviourExample = `  mtf-facts behaviour ./head --diff files.json --json > behaviour.json
  mtf-facts behaviour ./head --base ./base --schema schema.json --json`
	behaviourBaseUsage      = "base `tree` the change started from"
	behaviourDiffUsage      = "change as a GitHub pull request files list or a unified diff `file`"
	behaviourSchemaUsage    = "terraform providers schema -json `file` that decides Optional and Computed"
	behaviourChangeRequired = "give the change with --base or --diff"
)

const (
	claimsEvalShort = "Validate review claims and evaluate them and the triggers against a ladder record"
	claimsEvalLong  = `Validate the claims file against the closed predicate schema, discarding each
invalid claim with a reason, map each claim to the trigger its params.node
names, and compute every claim's and trigger's state from the ladder record.

A claim is verified when its level's runs with a valid control have
observations about it and all agree, refuted when one contradicts it, and
unverifiable otherwise; an unknown, sensitive or redacted value leaves a value
claim unverifiable. A trigger is refuted when one of its claims is. A trigger
that selects L1 or L3 is decided by the tool's own base and head sweep from
runs with a valid control: verified when no case differs, or when verified
claims state both sides of every difference exactly; each difference is
listed in its changes. A trigger that selects L4 is covered when its address
and changed attribute are in a planned example and no claim of it is
unverifiable. An inert entry's triggers are inert; anything else is
unverifiable. Refuted outranks covered.

With --head-sha and --base-sha the record must name those commits, so a
record of another run cannot decide anything.

Exit 0 with the report, 2 on bad input or a record of other commits.`
	claimsEvalExample = `  mtf-facts claims-eval --behaviour behaviour.json --ladder ladder.json \
    --claims claims.json --json`
	claimsEvalBehaviourUsage = "behaviour --json report `file`"
	claimsEvalLadderUsage    = "ladder record `file`"
	claimsEvalClaimsUsage    = "claims `file` the reviewing model wrote"
	claimsEvalRequired       = "--behaviour and --ladder are required"
	claimsEvalHeadSHAUsage   = "head commit `sha` the ladder record must name; a record of another commit is refused"
	claimsEvalBaseSHAUsage   = "base commit `sha` the ladder record must name; a record of another commit is refused"
)
