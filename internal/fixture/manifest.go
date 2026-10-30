package fixture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Manifest is what the trusted template says each file of the tree holds:
// its exact top-level block set, and the attributes that each hold one
// spliced expression.
type Manifest struct {
	SchemaVersion int                     `json:"schema_version,omitempty"` // 1 when set
	Files         map[string]ManifestFile `json:"files"`
}

// ManifestFile is the expected content of one file.
type ManifestFile struct {
	Blocks            []BlockRef   `json:"blocks"`
	SingleExpressions []SingleExpr `json:"single_expressions,omitempty"`
}

// BlockRef names a top-level block by type and labels.
type BlockRef struct {
	Type   string   `json:"type"`
	Labels []string `json:"labels"`
}

// SingleExpr is an attribute of a listed block that holds exactly one
// expression and nothing after it: it must be the last item of its block.
type SingleExpr struct {
	Block     string   `json:"block"`
	Labels    []string `json:"labels"`
	Attribute string   `json:"attribute"`
}

const maxManifestBytes = 1 << 20

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func (b BlockRef) key() string { return blockKey(b.Type, b.Labels) }

func blockKey(typ string, labels []string) string {
	return typ + "\x00" + strings.Join(labels, "\x00")
}

// ReadManifest reads and checks a manifest file.
func ReadManifest(name string) (*Manifest, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("expect: over %d bytes", maxManifestBytes)
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("expect: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("expect: data after the document")
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("expect: %w", err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	if m.SchemaVersion != 0 && m.SchemaVersion != 1 {
		return fmt.Errorf("schema_version %d, want 1", m.SchemaVersion)
	}
	if m.Files == nil {
		return errors.New("files is required")
	}
	for _, name := range slices.Sorted(maps.Keys(m.Files)) {
		if !tfName.MatchString(name) {
			return fmt.Errorf("file %q is not a .tf file name", name)
		}
		mf := m.Files[name]
		listed := map[string]bool{}
		for _, b := range mf.Blocks {
			if !identifier.MatchString(b.Type) || b.Labels == nil {
				return fmt.Errorf("%s: a block needs a type and a labels list", name)
			}
			listed[b.key()] = true
		}
		for _, s := range mf.SingleExpressions {
			if !identifier.MatchString(s.Attribute) {
				return fmt.Errorf("%s: attribute %q is not an identifier", name, s.Attribute)
			}
			if !listed[blockKey(s.Block, s.Labels)] {
				return fmt.Errorf("%s: single expression %s.%s is in no listed block", name, s.Block, s.Attribute)
			}
		}
	}
	return nil
}

// expect compares the parsed files with the manifest.
func (c *checker) expect(m *Manifest) {
	for _, name := range slices.Sorted(maps.Keys(c.bodies)) {
		if _, ok := m.Files[name]; !ok {
			c.add(name, 0, RuleManifestBlockSet, "")
		}
	}
	for _, name := range slices.Sorted(maps.Keys(m.Files)) {
		body, ok := c.bodies[name]
		if !ok {
			c.add(name, 0, RuleManifestBlockSet, "")
			continue
		}
		mf := m.Files[name]
		c.blockSet(name, body, mf.Blocks)
		for _, s := range mf.SingleExpressions {
			c.singleExpr(name, body, s)
		}
	}
}

// blockSet reports each block beyond the expected count of its type and
// labels, and each expected block the file lacks.
func (c *checker) blockSet(file string, body *hclsyntax.Body, want []BlockRef) {
	left := map[string]int{}
	types := map[string]string{}
	for _, b := range want {
		left[b.key()]++
		types[b.key()] = b.Type
	}
	for _, b := range body.Blocks {
		k := blockKey(b.Type, b.Labels)
		if left[k] == 0 {
			c.add(file, b.TypeRange.Start.Line, RuleManifestBlockSet, b.Type)
			continue
		}
		left[k]--
	}
	for _, k := range slices.Sorted(maps.Keys(left)) {
		if left[k] > 0 {
			c.add(file, 0, RuleManifestBlockSet, types[k])
		}
	}
}

// singleExpr checks that the one block s names has the attribute as its
// last item: anything a spliced expression adds after it is refused.
func (c *checker) singleExpr(file string, body *hclsyntax.Body, s SingleExpr) {
	var match []*hclsyntax.Block
	for _, b := range body.Blocks {
		if b.Type == s.Block && slices.Equal(b.Labels, s.Labels) {
			match = append(match, b)
		}
	}
	if len(match) != 1 {
		return // blockSet reports it
	}
	b := match[0]
	a := b.Body.Attributes[s.Attribute]
	if a == nil {
		c.add(file, b.TypeRange.Start.Line, RuleManifestExpression, s.Attribute)
		return
	}
	end := a.SrcRange.Start.Byte
	for _, other := range b.Body.Attributes {
		if other != a && other.SrcRange.Start.Byte > end {
			c.add(file, other.NameRange.Start.Line, RuleManifestExpression, s.Attribute)
		}
	}
	for _, nb := range b.Body.Blocks {
		if nb.TypeRange.Start.Byte > end {
			c.add(file, nb.TypeRange.Start.Line, RuleManifestExpression, s.Attribute)
		}
	}
}
