package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/modulestf/facts/internal/readcap"
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

// nameRawError returns err, from decoding the raw schema file data, with
// the provider and the type it refused named, as a whole decode named them.
// It reads the file again member by member, which costs memory, so it runs
// only after a decode failed. It returns err as it is when it finds no
// refused type or provider.
func nameRawError(data []byte, err error) error {
	var top struct {
		Schemas json.RawMessage `json:"provider_schemas"`
	}
	if json.Unmarshal(data, &top) != nil || len(top.Schemas) == 0 || isNull(top.Schemas) {
		return err
	}
	var named error
	_ = eachMember(top.Schemas, func(source string, v json.RawMessage) error {
		var p struct {
			R json.RawMessage `json:"resource_schemas"`
			D json.RawMessage `json:"data_source_schemas"`
		}
		_ = json.Unmarshal(v, &p) // a member of the wrong shape is found below
		for _, ts := range []struct {
			kind string
			raw  json.RawMessage
		}{{"resource", p.R}, {"data source", p.D}} {
			named = namedType(ts.raw, func(name string, t json.RawMessage) error {
				if _, err := decodeRawType(t); err != nil {
					return fmt.Errorf("provider %s: %s %q: %w", source, ts.kind, name, err)
				}
				return nil
			})
			if named != nil {
				return named
			}
		}
		if err := json.Unmarshal(v, new(rawProvider)); err != nil {
			named = fmt.Errorf("provider %s: %w", source, err)
		}
		return named
	})
	if named != nil {
		return named
	}
	return err
}

// namedType returns the first error check gives a type of the JSON object
// raw, nil when raw is absent, null or not an object.
func namedType(raw json.RawMessage, check func(name string, t json.RawMessage) error) error {
	if len(raw) == 0 || isNull(raw) {
		return nil
	}
	var named error
	_ = eachMember(raw, func(name string, t json.RawMessage) error {
		named = check(name, t)
		return named
	})
	return named
}

// isNull reports whether the JSON value b is null.
func isNull(b []byte) bool { return string(bytes.TrimSpace(b)) == "null" }

// eachMember calls f with each member of the JSON object b in order, a
// member written twice included, until f returns an error.
func eachMember(b []byte, f func(name string, v json.RawMessage) error) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	switch {
	case err != nil:
		return err
	case tok != json.Delim('{'):
		return errors.New("not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
		if err := f(tok.(string), v); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

// loadRaw reads each `terraform providers schema -json` file. A provider
// source in two files is an error: which one a lookup reads must not depend
// on the order of the flags. Each file is recorded by its base name, so the
// envelope holds no host path and the same file read from another directory
// hashes the same; two files with one base name are an error.
func loadRaw(files []string, keep Keep, deadline time.Time) (map[string]*types, []FileHash, error) {
	if len(files) > maxSchemaFiles {
		return nil, nil, fmt.Errorf("schema files: more than %d", maxSchemaFiles)
	}
	raw := map[string]*types{}
	hashes := []FileHash{}
	seen := map[string]bool{}
	for _, p := range files {
		if err := expired(deadline); err != nil {
			return nil, nil, err
		}
		name := filepath.Base(p)
		if seen[name] {
			return nil, nil, fmt.Errorf("schema file %s: another schema file has the base name %s", p, name)
		}
		seen[name] = true
		data, err := readcap.File(p, maxSchemaFileBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("schema file: %s: %w", p, err)
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
	if err := expired(deadline); err != nil {
		return nil, nil, err
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
		return nil, nameRawError(data, err)
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
