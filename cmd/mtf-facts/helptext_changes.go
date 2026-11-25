package main

const (
	changesShort = "Report the changed attributes of a change and what reads them"
	changesLong  = `Report what a change does to the HCL of a module tree, read with the HCL
parser. Give the tree the change started from with --base, or the change
itself with --diff, a GitHub pull request files list or a unified diff that
the base tree is rebuilt from. One of them is required.

files lists every .tf and .tf.json file whose entry differs, in path order,
with its status (added, removed, modified) and whether it was parsed; reason
says why not: json, override, parse-error, limit, duplicate-address, or the
kind of an entry that is not a regular file. outside is set when anything
changed besides the values of direct attributes of blocks present at both
sides: a comment or blank line between blocks, a block added, removed or
moved, or the file added, removed or not parsed.

blocks lists each top-level block that was added, removed or edited, by
address. structural is set for an edit besides the values of its direct
attributes: its header, a comment, an attribute added, removed or moved, or
any nested block. attributes lists the direct attributes present at both
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
tags_all reads tags, and self.<name> inside the block reads <name>. A
.tf.json, override, unparsed or non-regular file in that directory makes the
state unknown. Blocks of other kinds have state unknown. readers lists the
reads, at most 32 per attribute.

data_sources lists, per head directory with a configuration file, the data
source types its files declare. complete is false when a file there is a
.tf.json file, is not a regular file or does not parse, or a data block does
not have two labels. A directory reached through a link is not listed.

Exit codes:
  0  the report was written
  2  an error: bad input, a limit or the timeout; stdout is empty`
	changesExample = `  mtf-facts changes path/to/module --base path/to/base-tree --json
  mtf-facts changes path/to/module --diff files.json`
	changesJSONUsage = "write the report as JSON (schema/changes.schema.json)"
)
