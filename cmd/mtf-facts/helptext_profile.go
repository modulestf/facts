package main

// Help text of profile-check and profile-snapshot.

const (
	profileCheckShort = "Check a module profile and print its options, gates and downgrade rules"
	profileCheckLong  = `Read the module profile in --profile-dir as lint reads it, with no link
followed, and check it: the eight profile-constants blocks of PROFILE.md
with their keys, the templates they name, and the optional profile-options
block.

The profile-options block has five keys: format (1), consumers (public or
in-repo), partitions (portable or fixed), examples (required or none) and
release (registry or none). Without the block a profile has the defaults
public, portable, required and registry; any other value relaxes the
review. An unknown format, key or value is invalid, and so is examples: none
with any required-files example or example-dirs entry. Those two keys may be
written with no value ("example:") only with examples: none.

It prints one JSON object: valid, the problems, and for a valid profile its
options and whether they relax the review. --gates adds the gate table, the
rules each relaxing value makes not_applicable with the reason
profile_option, and the rules this profile gates. --downgrade-ids adds the
rule ids whose candidates carry the in-repo downgrade.

Exit 0 for a valid profile, 1 for an invalid one with the problems listed,
and 2 when the directory is missing, a link or cannot be read.`
	profileCheckExample = `  mtf-facts profile-check --profile-dir ./profile --gates --downgrade-ids`

	profileSnapshotShort = "Copy a module profile directory into a private temporary directory"
	profileSnapshotLong  = `Copy the profile directory --profile-dir into a new 0700 temporary
directory, so the options, the gates and the profile are read from one fixed
copy. Each directory is opened relative to its parent and each file is
opened without following a link or blocking, checked as the regular file
the listing saw and on the source's device, and copied from that one
descriptor. A link, a special file, a file on another device, more than 256
entries, more than 8 MiB or a path more than 8 levels deep is refused. The
copy is read back and compared before it is printed.

It prints one JSON object for the host: dir, the copy; tree_sha256, the
SHA-256 of sorted "<sha256>  <path>" lines over every file, which lint
writes as envelope.profile_tree_sha256; and profile_sha256, the profile's
own hash as lint writes it, null when the copy is not a valid profile. The
host removes the copy when it is done.

Exit 0 with the copy, 2 on a refusal or an unreadable directory.`
	profileSnapshotExample = `  mtf-facts profile-snapshot --profile-dir ./profile > snapshot.json`

	profileDirUsage       = "module profile `dir`; must exist and not be a link"
	profileGatesUsage     = "also print the gate table and the rules this profile gates"
	profileDowngradeUsage = "also print the rule ids of the in-repo downgrade"
	profileDirRequired    = "--profile-dir is required"
)
