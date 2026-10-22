package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	tfjson "github.com/hashicorp/terraform-json"
)

// Caps of the raw schema files. Variables so a test can lower them.
var (
	maxSchemaFiles           = 16
	maxSchemaFileBytes int64 = 128 << 20
)

// loadRaw reads each `terraform providers schema -json` file. A provider
// source in two files is an error: which one a lookup reads must not depend
// on the order of the flags.
func loadRaw(files []string) (map[string]*tfjson.ProviderSchema, []FileHash, error) {
	if len(files) > maxSchemaFiles {
		return nil, nil, fmt.Errorf("schema files: more than %d", maxSchemaFiles)
	}
	raw := map[string]*tfjson.ProviderSchema{}
	hashes := []FileHash{}
	for _, p := range files {
		data, err := readCapped(p, maxSchemaFileBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("schema file: %w", err)
		}
		var ps tfjson.ProviderSchemas
		// UnmarshalJSON also refuses a format_version it does not know.
		if err := ps.UnmarshalJSON(data); err != nil {
			return nil, nil, fmt.Errorf("schema file %s: malformed: %w", p, err)
		}
		for source, s := range ps.Schemas {
			if raw[source] != nil {
				return nil, nil, fmt.Errorf("schema file %s: provider %s is in another schema file too", p, source)
			}
			if s != nil {
				raw[source] = s
			}
		}
		sum := sha256.Sum256(data)
		hashes = append(hashes, FileHash{Path: p, SHA256: hex.EncodeToString(sum[:])})
	}
	return raw, hashes, nil
}
