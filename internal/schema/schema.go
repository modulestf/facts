// Package schema reads the schema handoff the schema job writes and raw
// `terraform providers schema -json` files, and answers lookups on them. It
// never fetches: every input is a local file, read under a size cap.
//
// A handoff answers only once Bind has matched it to the run's commits and
// .tf tree hashes. Until then, or when they differ, every handoff lookup is
// schema_unavailable. Raw schema files come from a workspace the user trusts
// and need no binding; they keep the attribute type and sensitive flag that
// the handoff drops.
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	tfjson "github.com/hashicorp/terraform-json"
)

// Outcome is the result of a lookup.
type Outcome string

// Lookup outcomes.
const (
	Present          Outcome = "present"
	AbsentFromSchema Outcome = "absent_from_schema"
	Unavailable      Outcome = "schema_unavailable"
)

// Run is what a handoff must match to be read: the commits and the tree
// hashes of the run's .tf inputs, in the handoff's tree_hash layout.
type Run struct {
	HeadSHA, MergeBaseSHA      string
	HeadTreeHash, BaseTreeHash string
}

// FileHash is the SHA-256 of one input file, keyed by the path it was read from.
type FileHash struct {
	Path   string
	SHA256 string
}

// Set is the loaded schema input of one run. A nil Set has no input, and
// every lookup on it is schema_unavailable.
type Set struct {
	// Handoff is the decoded handoff, nil without one.
	Handoff *Handoff
	// HandoffSHA256 hashes the handoff file as read, compressed or not.
	HandoffSHA256 string
	// SchemaFiles hashes each raw schema file, in the order given, by base name.
	SchemaFiles []FileHash
	// Mismatch says why the handoff does not cover the run, empty when it does.
	Mismatch string

	bound   bool
	dirs    map[string]map[string]*Provider // directory, local name
	schemas map[string]*types               // handoff, by schemaKey
	raw     map[string]*types               // source

	mu      sync.Mutex
	decoded map[decodedKey]*tfjson.SchemaBlock
}

// types are the resource and data source types of one provider schema, as
// JSON that decoded without error at load, and the decoder of one type.
type types struct {
	resources, dataSources map[string]json.RawMessage
	decode                 func(json.RawMessage) (*tfjson.SchemaBlock, error)
}

// decodedKey names one decoded type of one provider schema.
type decodedKey struct {
	types      *types
	dataSource bool
	name       string
}

// ErrDeadline is returned when Load passes its deadline.
var ErrDeadline = errors.New("deadline exceeded")

// now is the clock expired reads; a test replaces it.
var now = time.Now

// expired returns ErrDeadline once deadline has passed; a zero deadline never
// passes.
func expired(deadline time.Time) error {
	if !deadline.IsZero() && now().After(deadline) {
		return ErrDeadline
	}
	return nil
}

// Load reads the handoff at handoffPath, if not empty, and the raw schema
// files. Any read, cap or shape failure is an error: a caller must stop, not
// fall back. It stops with ErrDeadline once deadline passes, checked before
// and after the handoff and each raw file; zero means no deadline. The
// handoff is unbound until Bind.
func Load(handoffPath string, files []string, deadline time.Time) (*Set, error) {
	return load(handoffPath, files, nil, deadline)
}

// LoadTypes is Load of raw schema files alone that keeps only the types keep
// keeps. Every type is still checked, so a file Load refuses is refused
// here too. A lookup of a type keep did not keep is schema_unavailable: a
// caller must keep every type it looks up.
func LoadTypes(files []string, keep Keep, deadline time.Time) (*Set, error) {
	return load("", files, keep, deadline)
}

func load(handoffPath string, files []string, keep Keep, deadline time.Time) (*Set, error) {
	s := &Set{Mismatch: "not bound"}
	if handoffPath != "" {
		if err := expired(deadline); err != nil {
			return nil, err
		}
		h, sum, err := loadHandoff(handoffPath)
		if err != nil {
			return nil, fmt.Errorf("schema handoff: %w", err)
		}
		if err := expired(deadline); err != nil {
			return nil, err
		}
		s.Handoff, s.HandoffSHA256 = h, sum
		s.index()
	}
	raw, hashes, err := loadRaw(files, keep, deadline)
	if err != nil {
		return nil, err
	}
	s.raw, s.SchemaFiles = raw, hashes
	return s, nil
}

// Bind matches the handoff to the run. Every field must be set and equal:
// a run with no commits or no base tree is not covered.
func (s *Set) Bind(r Run) {
	h := s.Handoff
	switch {
	case h == nil:
		s.Mismatch = "no handoff"
	case r.HeadSHA == "" || r.HeadSHA != h.HeadSHA:
		s.Mismatch = "head SHA"
	case r.MergeBaseSHA == "" || r.MergeBaseSHA != h.MergeBaseSHA:
		s.Mismatch = "merge-base SHA"
	case r.HeadTreeHash == "" || r.HeadTreeHash != h.Inputs.Head.TreeHash:
		s.Mismatch = "head tree hash"
	case r.BaseTreeHash == "" || r.BaseTreeHash != h.Inputs.MergeBase.TreeHash:
		s.Mismatch = "merge-base tree hash"
	default:
		s.Mismatch = ""
	}
	s.bound = s.Mismatch == ""
}

// Resolution is the provider a directory names by a local name.
type Resolution struct {
	// Outcome is Present when the versions are resolved, Unavailable otherwise.
	Outcome Outcome
	// Reason is set when Outcome is Unavailable: the handoff's unresolved
	// reason, passed through, or schema_unavailable.
	Reason string
	// Source is set when the handoff names the provider, resolved or not.
	// Versions is set when Outcome is Present.
	Source   string
	Versions Versions
}

// Resolve returns the source and the latest, declared-minimum and old-floor
// versions of the provider dir names local.
func (s *Set) Resolve(dir, local string) Resolution {
	unavailable := Resolution{Outcome: Unavailable, Reason: string(Unavailable)}
	if s == nil || !s.bound {
		return unavailable
	}
	p := s.dirs[dir][local]
	switch {
	case p == nil:
		return unavailable
	case p.Versions == nil:
		return Resolution{Outcome: Unavailable, Reason: *p.UnresolvedReason, Source: p.Source}
	}
	return Resolution{Outcome: Present, Source: p.Source, Versions: *p.Versions}
}

// Query names a resource or data source type, and a path into it of
// attribute, nested attribute and nested block names. An empty path names
// the type itself.
type Query struct {
	Source string
	// Version picks the handoff's schema of that version. Empty reads the
	// raw schema files instead.
	Version    string
	DataSource bool
	Type       string
	Path       []string
}

// Field is what a present lookup found. Attribute is set for an attribute;
// BlockType and Block for a nested block; Block alone for the type itself.
type Field struct {
	Attribute *tfjson.SchemaAttribute
	BlockType *tfjson.SchemaBlockType
	Block     *tfjson.SchemaBlock
}

// Lookup answers q: present with the field, absent_from_schema when the
// schema of that provider version lacks the type or a name on the path, and
// schema_unavailable when there is no such schema or the handoff is unbound.
func (s *Set) Lookup(q Query) (Outcome, Field) {
	ts := s.provider(q.Source, q.Version)
	if ts == nil {
		return Unavailable, Field{}
	}
	b, err := s.block(ts, q.DataSource, q.Type)
	if err != nil {
		// A type LoadTypes did not keep. Each type decoded at load, so
		// the same bytes do not fail now.
		return Unavailable, Field{}
	}
	if b == nil {
		return AbsentFromSchema, Field{}
	}
	f, ok := walk(b, q.Path)
	if !ok {
		return AbsentFromSchema, Field{}
	}
	return Present, f
}

// block decodes the type name of ts on its first lookup and keeps it. It is
// nil when ts lacks the type or the type has no block.
func (s *Set) block(ts *types, dataSource bool, name string) (*tfjson.SchemaBlock, error) {
	m := ts.resources
	if dataSource {
		m = ts.dataSources
	}
	raw, ok := m[name]
	switch {
	case !ok:
		return nil, nil
	case raw == nil:
		return nil, errDropped
	}
	k := decodedKey{types: ts, dataSource: dataSource, name: name}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.decoded[k]; ok {
		return b, nil
	}
	b, err := ts.decode(raw)
	if err != nil {
		return nil, err
	}
	if s.decoded == nil {
		s.decoded = map[decodedKey]*tfjson.SchemaBlock{}
	}
	s.decoded[k] = b
	return b, nil
}

// rawMessages returns the types of a handoff provider schema as JSON.
func rawMessages(m map[string]HandoffType) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(m))
	for name, t := range m {
		out[name] = json.RawMessage(t)
	}
	return out
}

// errDropped is the lookup of a type LoadTypes did not keep.
var errDropped = errors.New("type not kept")

// rawBlock decodes one type of a raw schema file.
func rawBlock(raw json.RawMessage) (*tfjson.SchemaBlock, error) {
	t, err := decodeRawType(raw)
	if err != nil {
		return nil, err
	}
	return t.Block, nil
}

// handoffBlock decodes one type of the handoff to the terraform-json type
// lookups walk.
func handoffBlock(raw json.RawMessage) (*tfjson.SchemaBlock, error) {
	t, err := decodeHandoffType(raw)
	if err != nil {
		return nil, err
	}
	return t.Block.toTF(), nil
}

func (s *Set) provider(source, version string) *types {
	switch {
	case s == nil:
		return nil
	case version == "":
		return s.raw[source]
	case !s.bound:
		return nil
	}
	return s.schemas[schemaKey(source, version)]
}

func schemaKey(source, version string) string { return source + " " + version }

// walk follows path from b. A name is a nested block of a block, or an
// attribute of a block or of a nested attribute type.
func walk(b *tfjson.SchemaBlock, path []string) (Field, bool) {
	f := Field{Block: b}
	for _, name := range path {
		var attrs map[string]*tfjson.SchemaAttribute
		switch {
		case f.Block != nil:
			if bt := f.Block.NestedBlocks[name]; bt != nil && bt.Block != nil {
				f = Field{BlockType: bt, Block: bt.Block}
				continue
			}
			attrs = f.Block.Attributes
		case f.Attribute != nil && f.Attribute.AttributeNestedType != nil:
			attrs = f.Attribute.AttributeNestedType.Attributes
		}
		a := attrs[name]
		if a == nil {
			return Field{}, false
		}
		f = Field{Attribute: a}
	}
	return f, true
}

// index builds the lookup maps of a validated handoff.
func (s *Set) index() {
	s.dirs = map[string]map[string]*Provider{}
	for _, d := range s.Handoff.Directories {
		m := map[string]*Provider{}
		for i := range d.Providers {
			m[d.Providers[i].LocalName] = &d.Providers[i]
		}
		s.dirs[d.Path] = m
	}
	s.schemas = map[string]*types{}
	for _, p := range s.Handoff.ProviderSchemas {
		s.schemas[schemaKey(p.Source, p.Version)] = &types{
			resources: rawMessages(p.ResourceSchemas), dataSources: rawMessages(p.DataSourceSchemas), decode: handoffBlock,
		}
	}
}
