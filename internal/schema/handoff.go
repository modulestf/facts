package schema

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/hashicorp/go-version"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"
)

// Caps of the handoff, the producer's own: 16 MiB gzipped and 128 MiB of
// JSON. Variables so a test can lower them.
var (
	maxCompressedBytes int64 = 16 << 20
	maxHandoffBytes    int64 = 128 << 20
)

// maxBlockDepth bounds block and nested attribute nesting, above the
// producer's own limit of 32.
const maxBlockDepth = 64

// Handoff is schema/schema-handoff.schema.json. Pointers and slices mark
// required members: a nil one was missing or null.
type Handoff struct {
	HeadSHA         string           `json:"head_sha"`
	MergeBaseSHA    string           `json:"merge_base_sha"`
	Inputs          *Inputs          `json:"inputs"`
	Caps            *Caps            `json:"caps"`
	Directories     []Directory      `json:"directories"`
	ProviderSchemas []ProviderSchema `json:"provider_schemas"`
}

// Inputs are the .tf inputs the handoff was derived from, per revision.
type Inputs struct {
	Head      *TreeInputs `json:"head"`
	MergeBase *TreeInputs `json:"merge_base"`
}

// TreeInputs are the .tf files of one revision and the hash of their list.
type TreeInputs struct {
	TreeHash string      `json:"tree_hash"`
	Files    []InputFile `json:"files"`
}

// InputFile is one .tf input, its path relative to the repository root.
type InputFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Caps are the size caps the handoff was written under.
type Caps struct {
	CompressedBytes   int64 `json:"compressed_bytes"`
	UncompressedBytes int64 `json:"uncompressed_bytes"`
}

// Directory is one module directory and the providers it names.
type Directory struct {
	Path      string     `json:"path"`
	Providers []Provider `json:"providers"`
}

// Provider is one local name of a directory: its source and either the
// resolved versions or the reason they are unresolved.
type Provider struct {
	LocalName        string    `json:"local_name"`
	Source           string    `json:"source"`
	Versions         *Versions `json:"versions"`
	UnresolvedReason string    `json:"unresolved_reason"`
}

// Versions are the resolved versions of one provider in one directory.
// OldFloor is nil when the merge base does not name the provider there.
type Versions struct {
	Latest          string
	DeclaredMinimum string
	OldFloor        *string
}

// UnmarshalJSON requires old_floor to be present, as null or a version.
func (v *Versions) UnmarshalJSON(b []byte) error {
	var w struct {
		Latest          string          `json:"latest"`
		DeclaredMinimum string          `json:"declared_minimum"`
		OldFloor        json.RawMessage `json:"old_floor"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return err
	}
	if w.OldFloor == nil {
		return errors.New("versions: no old_floor")
	}
	v.Latest, v.DeclaredMinimum, v.OldFloor = w.Latest, w.DeclaredMinimum, nil
	return json.Unmarshal(w.OldFloor, &v.OldFloor)
}

// ProviderSchema is one provider version's filtered block schemas.
type ProviderSchema struct {
	Source            string                 `json:"source"`
	Version           string                 `json:"version"`
	ResourceSchemas   map[string]*TypeSchema `json:"resource_schemas"`
	DataSourceSchemas map[string]*TypeSchema `json:"data_source_schemas"`
}

// TypeSchema is one resource or data source type. The block is decoded into
// the terraform-json type, and validate refuses every field of it that the
// handoff does not carry.
type TypeSchema struct {
	Block *tfjson.SchemaBlock `json:"block"`
}

// loadHandoff reads, decompresses when gzipped, decodes and validates the
// handoff, and returns it with the SHA-256 of the file as read.
func loadHandoff(p string) (*Handoff, string, error) {
	data, err := readCapped(p, max(maxCompressedBytes, maxHandoffBytes))
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	compressed := len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
	doc := data
	if compressed {
		if doc, err = gunzip(data); err != nil {
			return nil, "", err
		}
	}
	if int64(len(doc)) > maxHandoffBytes {
		return nil, "", fmt.Errorf("over %d bytes", maxHandoffBytes)
	}
	var h Handoff
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return nil, "", fmt.Errorf("malformed: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, "", errors.New("malformed: data after the document")
	}
	if err := h.validate(); err != nil {
		return nil, "", fmt.Errorf("malformed: %w", err)
	}
	switch {
	case int64(len(doc)) > h.Caps.UncompressedBytes:
		return nil, "", fmt.Errorf("over its own cap of %d bytes", h.Caps.UncompressedBytes)
	case compressed && int64(len(data)) > h.Caps.CompressedBytes:
		return nil, "", fmt.Errorf("over its own cap of %d compressed bytes", h.Caps.CompressedBytes)
	}
	return &h, hex.EncodeToString(sum[:]), nil
}

func gunzip(data []byte) ([]byte, error) {
	if int64(len(data)) > maxCompressedBytes {
		return nil, fmt.Errorf("over %d compressed bytes", maxCompressedBytes)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	doc, err := io.ReadAll(io.LimitReader(zr, maxHandoffBytes+1))
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	return doc, nil
}

// Patterns of schema/schema-handoff.schema.json.
var (
	commitRe  = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	sha256Re  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
	sourceRe  = regexp.MustCompile(`^[a-z0-9.-]+/[a-z0-9_-]+/[a-z0-9_-]+$`)
	reasonRe  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	localRe   = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	typeRe    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// validate checks what decoding does not: required members, patterns,
// enums, the versions-or-reason choice, and uniqueness by key.
func (h *Handoff) validate() error {
	switch {
	case !commitRe.MatchString(h.HeadSHA) || !commitRe.MatchString(h.MergeBaseSHA):
		return errors.New("head_sha or merge_base_sha is not a commit")
	case h.Inputs == nil || h.Caps == nil || h.Directories == nil || h.ProviderSchemas == nil:
		return errors.New("a required member is missing")
	case h.Caps.CompressedBytes < 1 || h.Caps.UncompressedBytes < 1:
		return errors.New("caps must be at least 1")
	}
	for name, t := range map[string]*TreeInputs{"head": h.Inputs.Head, "merge_base": h.Inputs.MergeBase} {
		if err := t.validate(); err != nil {
			return fmt.Errorf("inputs.%s: %w", name, err)
		}
	}
	dirs := map[string]bool{}
	for _, d := range h.Directories {
		if !relPath(d.Path) || d.Providers == nil || dirs[d.Path] {
			return fmt.Errorf("directory %q: bad or repeated path, or no providers", d.Path)
		}
		dirs[d.Path] = true
		locals := map[string]bool{}
		for _, p := range d.Providers {
			if err := p.validate(); err != nil {
				return fmt.Errorf("directory %q provider %q: %w", d.Path, p.LocalName, err)
			}
			if locals[p.LocalName] {
				return fmt.Errorf("directory %q: local name %q repeated", d.Path, p.LocalName)
			}
			locals[p.LocalName] = true
		}
	}
	seen := map[string]bool{}
	for _, p := range h.ProviderSchemas {
		key := schemaKey(p.Source, p.Version)
		switch {
		case !sourceRe.MatchString(p.Source) || !versionRe.MatchString(p.Version) || seen[key]:
			return fmt.Errorf("provider schema %q: bad or repeated source and version", key)
		case p.ResourceSchemas == nil || p.DataSourceSchemas == nil:
			return fmt.Errorf("provider schema %q: a required member is missing", key)
		}
		seen[key] = true
		for _, types := range []map[string]*TypeSchema{p.ResourceSchemas, p.DataSourceSchemas} {
			for name, t := range types {
				if !typeRe.MatchString(name) || t == nil || t.Block == nil {
					return fmt.Errorf("provider schema %q type %q: bad name or no block", key, name)
				}
				if err := checkBlock(t.Block, 0); err != nil {
					return fmt.Errorf("provider schema %q type %q: %w", key, name, err)
				}
			}
		}
	}
	return nil
}

func (t *TreeInputs) validate() error {
	if t == nil || t.Files == nil || !sha256Re.MatchString(t.TreeHash) {
		return errors.New("missing, or a bad tree_hash")
	}
	paths := map[string]bool{}
	for _, f := range t.Files {
		if !relPath(f.Path) || !strings.HasSuffix(f.Path, ".tf") || !sha256Re.MatchString(f.SHA256) || paths[f.Path] {
			return fmt.Errorf("file %q: bad or repeated", f.Path)
		}
		paths[f.Path] = true
	}
	return nil
}

func (p Provider) validate() error {
	switch {
	case !localRe.MatchString(p.LocalName) || !sourceRe.MatchString(p.Source):
		return errors.New("bad local_name or source")
	case (p.Versions == nil) == (p.UnresolvedReason == ""):
		return errors.New("needs exactly one of versions and unresolved_reason")
	case p.Versions == nil:
		if !reasonRe.MatchString(p.UnresolvedReason) {
			return errors.New("bad unresolved_reason")
		}
		return nil
	}
	v := p.Versions
	if !versionRe.MatchString(v.Latest) || !versionRe.MatchString(v.DeclaredMinimum) ||
		v.OldFloor != nil && !versionRe.MatchString(*v.OldFloor) {
		return errors.New("bad version")
	}
	// The minimum and the latest both satisfy the head constraints, the
	// minimum being the lowest that does.
	if version.Must(version.NewVersion(v.DeclaredMinimum)).GreaterThan(version.Must(version.NewVersion(v.Latest))) {
		return errors.New("declared_minimum is above latest")
	}
	return nil
}

var (
	blockModes  = map[tfjson.SchemaNestingMode]bool{"single": true, "group": true, "list": true, "set": true, "map": true}
	nestedModes = map[tfjson.SchemaNestingMode]bool{"single": true, "list": true, "set": true, "map": true}
)

// checkBlock refuses the terraform-json fields the handoff never carries,
// and checks the nesting modes and required members.
func checkBlock(b *tfjson.SchemaBlock, depth int) error {
	if depth > maxBlockDepth {
		return errors.New("nested too deep")
	}
	if b.Description != "" || b.DescriptionKind != "" {
		return errors.New("a block has a description")
	}
	if err := checkAttributes(b.Attributes, depth); err != nil {
		return err
	}
	for name, bt := range b.NestedBlocks {
		if bt == nil || bt.Block == nil || !blockModes[bt.NestingMode] {
			return fmt.Errorf("block type %q: no block or a bad nesting_mode", name)
		}
		if err := checkBlock(bt.Block, depth+1); err != nil {
			return fmt.Errorf("block type %q: %w", name, err)
		}
	}
	return nil
}

func checkAttributes(attrs map[string]*tfjson.SchemaAttribute, depth int) error {
	for name, a := range attrs {
		switch {
		case a == nil:
			return fmt.Errorf("attribute %q is null", name)
		case a.AttributeType != cty.NilType || a.Description != "" || a.DescriptionKind != "" || a.Sensitive || a.WriteOnly:
			return fmt.Errorf("attribute %q has a field the handoff does not carry", name)
		case a.AttributeNestedType == nil:
			continue
		}
		nt := a.AttributeNestedType
		if nt.Attributes == nil || !nestedModes[nt.NestingMode] {
			return fmt.Errorf("attribute %q: nested_type with no attributes or a bad nesting_mode", name)
		}
		if depth+1 > maxBlockDepth {
			return errors.New("nested too deep")
		}
		if err := checkAttributes(nt.Attributes, depth+1); err != nil {
			return fmt.Errorf("attribute %q: %w", name, err)
		}
	}
	return nil
}

// relPath reports whether p is a relative path with no backslash, no ..
// segment and no trailing slash, as the handoff schema's path patterns say.
func relPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") || strings.Contains(p, `\`) {
		return false
	}
	for seg := range strings.SplitSeq(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}
