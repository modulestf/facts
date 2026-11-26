package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"

	tfjson "github.com/hashicorp/terraform-json"
)

// Caps of the raw schema files. Variables so a test can lower them.
var (
	maxSchemaFiles           = 16
	maxSchemaFileBytes int64 = 128 << 20
)

// rawFile is a `terraform providers schema -json` file with each resource
// and data source type kept as JSON, so a run holds the decoded schema of
// only the types it looks up.
type rawFile struct {
	FormatVersion string                  `json:"format_version"`
	Schemas       map[string]*rawProvider `json:"provider_schemas"`
}

// rawProvider is one provider of a raw schema file. The embedded
// ProviderSchema decodes every other member, so a bad shape there is still
// an error; the two maps shadow its members of the same JSON names.
type rawProvider struct {
	tfjson.ProviderSchema
	ResourceSchemas   map[string]rawType `json:"resource_schemas"`
	DataSourceSchemas map[string]rawType `json:"data_source_schemas"`

	resources, dataSources map[string]json.RawMessage // the types parseRaw keeps
}

// rawType is one type of a raw schema file as JSON. Decoding it checks it,
// so a type written twice under one name is checked both times, as a whole
// decode checks it.
type rawType []byte

func (t *rawType) UnmarshalJSON(b []byte) error {
	if _, err := decodeRawType(b); err != nil {
		return err
	}
	*t = bytes.Clone(b) // the decoder may reuse b
	return nil
}

// loadRaw reads each `terraform providers schema -json` file. A provider
// source in two files is an error: which one a lookup reads must not depend
// on the order of the flags. Each file is recorded by its base name, so the
// envelope holds no host path and the same file read from another directory
// hashes the same; two files with one base name are an error.
func loadRaw(files []string, keep Keep) (map[string]*types, []FileHash, error) {
	if len(files) > maxSchemaFiles {
		return nil, nil, fmt.Errorf("schema files: more than %d", maxSchemaFiles)
	}
	raw := map[string]*types{}
	hashes := []FileHash{}
	seen := map[string]bool{}
	for _, p := range files {
		name := filepath.Base(p)
		if seen[name] {
			return nil, nil, fmt.Errorf("schema file %s: another schema file has the base name %s", p, name)
		}
		seen[name] = true
		data, err := readCapped(p, maxSchemaFileBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("schema file: %w", err)
		}
		f, err := parseRaw(data, keep)
		if err != nil {
			return nil, nil, fmt.Errorf("schema file %s: malformed: %w", p, err)
		}
		for source, s := range f.Schemas {
			if raw[source] != nil {
				return nil, nil, fmt.Errorf("schema file %s: provider %s is in another schema file too", p, source)
			}
			if s != nil {
				raw[source] = &types{resources: s.resources, dataSources: s.dataSources, decode: rawBlock}
			}
		}
		sum := sha256.Sum256(data)
		hashes = append(hashes, FileHash{Path: name, SHA256: hex.EncodeToString(sum[:])})
	}
	return raw, hashes, nil
}

// Keep reports whether a run looks up a type of a raw schema file. A type
// it does not keep is checked at load like any other and then dropped.
type Keep func(dataSource bool, name string) bool

// parseRaw decodes a raw schema file and refuses what decoding it whole into
// tfjson.ProviderSchemas refuses: bad JSON, a member of the wrong shape in
// any type, used or not, and a format_version it does not know. Each type
// is decoded on its own and dropped, so the peak holds one decoded type. A
// type keep, when not nil, does not keep stays in the map with a nil value.
func parseRaw(data []byte, keep Keep) (*rawFile, error) {
	var f rawFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	for _, p := range f.Schemas {
		if p == nil {
			continue
		}
		p.ProviderSchema = tfjson.ProviderSchema{} // decoded for its shape only
		p.resources = kept(p.ResourceSchemas, false, keep)
		p.dataSources = kept(p.DataSourceSchemas, true, keep)
		p.ResourceSchemas, p.DataSourceSchemas = nil, nil
	}
	ps := tfjson.ProviderSchemas{FormatVersion: f.FormatVersion}
	if err := ps.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// kept returns the types of m with a nil value for each type keep, when
// not nil, does not keep. A type written as null is kept as null.
func kept(m map[string]rawType, dataSource bool, keep Keep) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(m))
	for name, t := range m {
		switch {
		case keep != nil && !keep(dataSource, name):
			out[name] = nil
		case t == nil:
			out[name] = json.RawMessage("null")
		default:
			out[name] = json.RawMessage(t)
		}
	}
	return out
}

// decodeRawType decodes one type of a raw schema file.
func decodeRawType(t json.RawMessage) (*tfjson.Schema, error) {
	var s tfjson.Schema
	if err := json.Unmarshal(t, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
