// Package profile parses a module profile: the fenced profile-constants
// blocks of its PROFILE.md and the templates those blocks name. It reads no
// files itself; the caller hands it the bytes of a confined read.
package profile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
)

// File is the profile file that holds the constants blocks, relative to the
// profile directory.
const File = "PROFILE.md"

// TemplateDir is the directory, relative to the profile directory, that holds
// the templates the template-destinations block names.
const TemplateDir = "templates"

// Profile is the parsed constants of one module profile. List values keep the
// order the block writes them in.
type Profile struct {
	// SHA256 hashes the files the profile was built from. See Preimage.
	SHA256 string

	RequiredFiles RequiredFiles
	Regions       Regions
	ArgumentOrder ArgumentOrder
	// Templates are the template-destinations block, in block order, each
	// with the bytes of its template.
	Templates    []Template
	ProfileOwned ProfileOwned
	Tooling      Tooling
	Banner       string
	AdjunctTypes []string
	// Options are the profile-options block, or DefaultOptions without one.
	Options Options
}

// RequiredFiles are the files each area kind starts with, relative to the
// area, and the example directories a module starts with.
type RequiredFiles struct {
	Root, Submodule, Example, ExampleDirs []string
}

// Regions are the two regions the profile allows.
type Regions struct {
	Primary, Secondary string
}

// ArgumentOrder are the arguments that may hold the first and the last
// position of a resource block.
type ArgumentOrder struct {
	First, Last []string
}

// Template is one template file and its destination relative to the module
// root.
type Template struct {
	Name        string // file name under templates/
	Destination string
	Data        []byte
}

// ProfileOwned are the files the profile owns verbatim and the directory
// whose file set is exactly its workflows.
type ProfileOwned struct {
	Verbatim    []string
	WorkflowDir string
}

// Tooling is the closed list of scanner and linter files and directories.
type Tooling struct {
	Files, Dirs []string
}

// blockKeys are the blocks a profile must have and the fixed keys of each.
// template-destinations is keyed by template file name instead.
var blockKeys = map[string][]string{
	"required-files":        {"root", "submodule", "example", "example-dirs"},
	"regions":               {"primary", "secondary"},
	"argument-order":        {"first", "last"},
	"template-destinations": nil,
	"profile-owned-files":   {"verbatim", "workflow-dir"},
	"tooling":               {"files", "dirs"},
	"banner":                {"line"},
	"adjunct-types":         {"types"},
}

const (
	fenceOpen  = "```profile-constants"
	fenceClose = "```"
)

var (
	keyLine = regexp.MustCompile(`^([A-Za-z0-9._-]+): (.*)$`)
	// emptyLine is a key with no value, which only emptyAllowed may have.
	emptyLine = regexp.MustCompile(`^([A-Za-z0-9._-]+):$`)
)

// emptyAllowed are the keys, as block.key, that may be written with no
// value: an empty list, or a single value that is not set. The example
// entries may be empty only with examples: none.
var emptyAllowed = map[string]bool{
	"required-files.example": true, "required-files.example-dirs": true,
	"profile-owned-files.verbatim": true, "profile-owned-files.workflow-dir": true,
	"tooling.files": true, "tooling.dirs": true, "banner.line": true,
}

// block is one parsed constants block: its keys in file order and values.
type block struct {
	keys   []string
	values map[string]string
}

// New builds a profile from files, the bytes of the profile directory's files
// keyed by slash path relative to it. It needs PROFILE.md and every template
// the template-destinations block names, under templates/. Any other file is
// ignored and does not count toward SHA256.
func New(files map[string][]byte) (*Profile, error) {
	doc, ok := files[File]
	if !ok {
		return nil, fmt.Errorf("no %s", File)
	}
	blocks, err := parseBlocks(doc)
	if err != nil {
		return nil, err
	}
	p, err := fromBlocks(blocks)
	if err != nil {
		return nil, err
	}
	used := map[string][]byte{File: doc}
	for i, t := range p.Templates {
		name := path.Join(TemplateDir, t.Name)
		data, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("template-destinations: no %s", name)
		}
		p.Templates[i].Data = data
		used[name] = data
	}
	p.SHA256 = hashFiles(used)
	return p, nil
}

// Preimage is the input of Profile.SHA256: one line per file the profile was
// built from, PROFILE.md and templates/<name> for each template the
// template-destinations block names, written as <sha256><two spaces><path><LF>
// with the path relative to the profile directory, the lines sorted by the
// bytes of the path and no other bytes. It is the layout of sha256sum output
// and of the schema handoff's tree_hash.
func Preimage(files map[string][]byte) []byte {
	var b bytes.Buffer
	for _, p := range slices.Sorted(maps.Keys(files)) {
		sum := sha256.Sum256(files[p])
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), p)
	}
	return b.Bytes()
}

// TreeSHA256 is the tree hash of a profile directory: the SHA-256 of the
// Preimage of every one of its files, not only those the profile is built
// from.
func TreeSHA256(files map[string][]byte) string { return hashFiles(files) }

func hashFiles(files map[string][]byte) string {
	sum := sha256.Sum256(Preimage(files))
	return hex.EncodeToString(sum[:])
}

// parseBlocks reads every profile-constants block of doc. The grammar is the
// one PROFILE.md states: the first line is name: <block name>, each later line
// <key>: <value>, a block name once per file and a key once per block, and no
// double space or trailing space in a value. A later line may also be <key>:
// with no value; fromBlocks refuses it outside emptyAllowed.
func parseBlocks(doc []byte) (map[string]*block, error) {
	blocks := map[string]*block{}
	var cur *block
	var name string
	// Split on LF alone, so a CR stays in the line and fails the value check.
	for i, raw := range bytes.Split(doc, []byte("\n")) {
		n, line := i+1, string(raw)
		switch {
		case line == fenceOpen:
			if cur != nil {
				return nil, fmt.Errorf("%s line %d: a block opens inside another", File, n)
			}
			cur, name = &block{values: map[string]string{}}, ""
			continue
		case cur == nil:
			continue
		case line == fenceClose:
			if name == "" {
				return nil, fmt.Errorf("%s line %d: a block with no name line", File, n)
			}
			blocks[name], cur = cur, nil
			continue
		}
		m := keyLine.FindStringSubmatch(line)
		empty := false
		if m == nil {
			if m = emptyLine.FindStringSubmatch(line); m == nil || name == "" {
				return nil, fmt.Errorf("%s line %d: not <key>: <value>", File, n)
			}
			m, empty = append(m, ""), true
		}
		k, v := m[1], m[2]
		if name == "" {
			if k != "name" {
				return nil, fmt.Errorf("%s line %d: the first line is not the name line", File, n)
			}
			if _, dup := blocks[v]; dup {
				return nil, fmt.Errorf("%s line %d: block name %q used twice", File, n, v)
			}
			name = v
			continue
		}
		if _, dup := cur.values[k]; dup {
			return nil, fmt.Errorf("%s line %d: block %s: key %s used twice", File, n, name, k)
		}
		if !empty && v == "" || strings.Contains(v, "  ") || strings.HasSuffix(v, " ") || strings.ContainsAny(v, "\r\t") {
			return nil, fmt.Errorf("%s line %d: block %s: key %s has an empty value or a stray space", File, n, name, k)
		}
		cur.keys = append(cur.keys, k)
		cur.values[k] = v
	}
	if cur != nil {
		return nil, fmt.Errorf("%s: a block is not closed", File)
	}
	return blocks, nil
}

// fromBlocks checks the block set and each block's keys, and builds the
// profile. A missing or unknown block or key is an error.
func fromBlocks(blocks map[string]*block) (*Profile, error) {
	for name := range blocks {
		if _, ok := blockKeys[name]; !ok && name != optionsBlock {
			return nil, fmt.Errorf("unknown block %s", name)
		}
	}
	for name, b := range blocks {
		for _, k := range b.keys {
			if b.values[k] == "" && !emptyAllowed[name+"."+k] {
				return nil, fmt.Errorf("block %s: key %s has an empty value", name, k)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(blockKeys)) {
		b, ok := blocks[name]
		if !ok {
			return nil, fmt.Errorf("no block %s", name)
		}
		if want := blockKeys[name]; want != nil {
			if err := checkKeys(name, b, want); err != nil {
				return nil, err
			}
		}
	}
	list := func(name, key string) []string {
		if v := blocks[name].values[key]; v != "" {
			return strings.Split(v, " ")
		}
		return nil
	}
	one := func(name, key string) (string, error) {
		v := blocks[name].values[key]
		if strings.Contains(v, " ") {
			return "", fmt.Errorf("block %s: key %s holds more than one value", name, key)
		}
		return v, nil
	}
	p := &Profile{
		RequiredFiles: RequiredFiles{
			Root:        list("required-files", "root"),
			Submodule:   list("required-files", "submodule"),
			Example:     list("required-files", "example"),
			ExampleDirs: list("required-files", "example-dirs"),
		},
		ArgumentOrder: ArgumentOrder{First: list("argument-order", "first"), Last: list("argument-order", "last")},
		ProfileOwned:  ProfileOwned{Verbatim: list("profile-owned-files", "verbatim")},
		Tooling:       Tooling{Files: list("tooling", "files"), Dirs: list("tooling", "dirs")},
		Banner:        blocks["banner"].values["line"],
		AdjunctTypes:  list("adjunct-types", "types"),
	}
	var err error
	if p.Options, err = optionsFrom(blocks); err != nil {
		return nil, err
	}
	exampleEntries := len(p.RequiredFiles.Example) + len(p.RequiredFiles.ExampleDirs)
	switch {
	case p.Options.Examples == "none" && exampleEntries > 0:
		return nil, fmt.Errorf("block %s: examples: none, but required-files names example files or directories", optionsBlock)
	case p.Options.Examples != "none" && (p.RequiredFiles.Example == nil || p.RequiredFiles.ExampleDirs == nil):
		return nil, errors.New("block required-files: key example or example-dirs has an empty value, but examples are required")
	}
	if p.Regions.Primary, err = one("regions", "primary"); err != nil {
		return nil, err
	}
	if p.Regions.Secondary, err = one("regions", "secondary"); err != nil {
		return nil, err
	}
	if p.ProfileOwned.WorkflowDir, err = one("profile-owned-files", "workflow-dir"); err != nil {
		return nil, err
	}
	paths := slices.Concat(p.RequiredFiles.Root, p.RequiredFiles.Submodule, p.RequiredFiles.Example,
		p.RequiredFiles.ExampleDirs, p.ProfileOwned.Verbatim, p.Tooling.Files, p.Tooling.Dirs)
	if p.ProfileOwned.WorkflowDir != "" {
		paths = append(paths, p.ProfileOwned.WorkflowDir)
	}
	for _, v := range paths {
		if !safePath(v) {
			return nil, fmt.Errorf("unsafe path %q", v)
		}
	}
	// A profile may ship no template: the block then has no key.
	for _, k := range blocks["template-destinations"].keys {
		v, err := one("template-destinations", k)
		if err != nil {
			return nil, err
		}
		// keyLine already keeps "/" out of a key, so safePath(k) leaves a
		// plain file name.
		if !safePath(k) || !safePath(v) {
			return nil, fmt.Errorf("block template-destinations: unsafe template or destination %q: %q", k, v)
		}
		p.Templates = append(p.Templates, Template{Name: k, Destination: v})
	}
	return p, nil
}

func checkKeys(name string, b *block, want []string) error {
	for _, k := range b.keys {
		if !slices.Contains(want, k) {
			return fmt.Errorf("block %s: unknown key %s", name, k)
		}
	}
	for _, k := range want {
		if _, ok := b.values[k]; !ok {
			return fmt.Errorf("block %s: missing key %s", name, k)
		}
	}
	return nil
}

// safePath reports whether p is a clean relative slash path with no . or ..
// component. A value may hold a backslash or a NUL, so both are refused here.
func safePath(p string) bool {
	if p == "" || path.IsAbs(p) || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	for c := range strings.SplitSeq(p, "/") {
		if c == "" || c == "." || c == ".." {
			return false
		}
	}
	return true
}
