package main

const (
	changesShort = "Report the changed attributes of a change and what reads them"
	changesLong  = `Report what a change does to the HCL of a module tree, read with the HCL
parser. Give the tree the change started from with --base, or the change
itself with --diff, a GitHub pull request files list or a unified diff that
the base tree is rebuilt from. One of them is required.

files lists every .tf, .tf.json, .tofu and .tofu.json file whose entry
differs, in path order, with its status (added, removed, modified) and
whether it was parsed; reason says why not: json, tofu, override,
parse-error, limit, duplicate-address (two resource, data, module, variable
or output blocks of one address), or the kind of an entry that is not a
regular file. Other changed files, templates and .tfvars among them, are
not listed: the host classifies them itself. outside is set when anything
changed besides the values of direct attributes of blocks present at both
sides: a comment or blank line between blocks, a block added, removed or
moved, or the file added, removed or not parsed.

blocks lists each top-level block that was added, removed or edited, by
address. structural is set for an edit besides the values of its direct
attributes: its header, a comment, an attribute added, removed or moved, any
nested block, or, in a resource or data block, a change of count, for_each,
provider or depends_on. Blocks of a type that may repeat, such as locals or
moved, are matched in order. attributes lists the direct attributes present at both
sides whose value bytes changed. plain says, per side, whether the value is
a plain expression: literals, a negative number, quoted strings with plain
interpolations and no directive, references not rooted at each, count or
self, tuples, objects with name or literal string keys, and calls of only
concat, compact, flatten, distinct, join, format, length, toset, tolist,
tomap, element, keys and values.

read says whether an expression of the module that declares a resource or
data block reads a changed attribute of it at head: read, unread or unknown.
Only the configuration files directly in that directory are read, since an
address resolves only inside its module; comments and string text are no
reads. A reference reads the attribute it names after the address, past one
index or splat that selects instances of a block with count or for_each; an
index with a literal string key names an attribute; the whole object, a for
expression over it, and any other index or splat read every attribute.
tags_all reads tags, self.<name> inside the block reads <name>, and
resource.<type>.<name> is read as <type>.<name>. Reads are by name or by the
whole object: a derived (computed) attribute such as id or arn, or an ignore_changes
entry, reads no other attribute. A
.tf.json, .tofu, .tofu.json, override, unparsed or non-regular file in that
directory makes the state unknown. Blocks of other kinds, and resource or data blocks without two labels,
have state unknown. .tftest.hcl files are not scanned. readers lists the
reads, at most 32 per attribute.

data_sources lists, per head directory with a configuration file, the data
source types its files declare, inside check blocks too. complete is false
when a file there is a .tf.json, .tofu or .tofu.json file, is not a regular
file or does not parse, or a data block does
not have two labels. A directory reached through a link is not listed.

Exit codes:
  0  the report was written
  2  an error: bad input, a limit or the timeout; stdout is empty`
	changesExample = `  mtf-facts changes path/to/module --base path/to/base-tree --json
  mtf-facts changes path/to/module --diff files.json`
	changesJSONUsage = "write the report as JSON (schema/changes.schema.json)"
)
