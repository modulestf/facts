package benchtree

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
)

// HeadSHA and BaseSHA are the commits the handoff of WriteHandoff claims.
// A run binds the handoff only when it gives the same commits.
const (
	HeadSHA = "1111111111111111111111111111111111111111"
	BaseSHA = "2222222222222222222222222222222222222222"
)

// Size of the generated provider schema: the types the tree uses, plus
// unrelated types up to the size of a large provider. Each unrelated type
// has genAttributes attributes and genBlocks nested blocks of
// genBlockAttributes attributes, each block with one more nested block.
const (
	genResources       = 1500
	genDataSources     = 650
	genAttributes      = 64
	genBlocks          = 4
	genBlockAttributes = 16
)

// Versions of the aws provider the handoff resolves. The older one lacks
// every fifth attribute of the unrelated types and ingress_limit.
const (
	awsSource     = "registry.terraform.io/hashicorp/aws"
	tlsSource     = "registry.terraform.io/hashicorp/tls"
	awsLatest     = "6.2.0"
	awsMinimum    = "6.0.0"
	tlsVersion    = "4.0.6"
	handoffCapGz  = 16 << 20
	handoffCapRaw = 128 << 20
)

// WriteHandoff writes a gzipped schema handoff, in the shape of
// schema/schema-handoff.schema.json, for the head and base trees of Write.
// It binds to a run of those trees with HeadSHA and BaseSHA. Its JSON is
// tens of MB, about the size of the aws provider at two versions.
func WriteHandoff(path string) error {
	data, err := handoffBytes()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// handoffBytes builds the gzipped handoff once per process: a benchmark
// run with -count writes it again for every count.
var handoffBytes = sync.OnceValues(func() ([]byte, error) {
	doc, err := json.Marshal(handoff(genResources, genDataSources))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf) // no name and no time in the header: same bytes every run
	if _, err := zw.Write(doc); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if buf.Len() > handoffCapGz || len(doc) > handoffCapRaw {
		return nil, fmt.Errorf("handoff of %d bytes, %d gzipped, is over its caps", len(doc), buf.Len())
	}
	return buf.Bytes(), nil
})

// handoff is the handoff document with res and data unrelated resource and
// data source types.
func handoff(res, data int) map[string]any {
	var dirs []map[string]any
	for _, d := range moduleDirs() {
		dirs = append(dirs, map[string]any{"path": d, "providers": []map[string]any{
			{"local_name": "aws", "source": awsSource, "versions": map[string]any{"latest": awsLatest, "declared_minimum": awsMinimum, "old_floor": awsMinimum}},
			{"local_name": "tls", "source": tlsSource, "versions": map[string]any{"latest": tlsVersion, "declared_minimum": tlsVersion, "old_floor": tlsVersion}},
		}})
	}
	return map[string]any{
		"head_sha":       HeadSHA,
		"merge_base_sha": BaseSHA,
		"inputs":         map[string]any{"head": treeInputs(tree(false)), "merge_base": treeInputs(tree(true))},
		"caps":           map[string]any{"compressed_bytes": handoffCapGz, "uncompressed_bytes": handoffCapRaw},
		"directories":    dirs,
		"provider_schemas": []map[string]any{
			awsSchema(awsLatest, false, res, data),
			awsSchema(awsMinimum, true, res, data),
			{"source": tlsSource, "version": tlsVersion, "resource_schemas": map[string]any{
				"tls_private_key": typeSchema(attrs("required", "algorithm").with("optional", "rsa_bits").with("computed", "id", "private_key_pem", "public_key_pem"), nil),
			}, "data_source_schemas": map[string]any{}},
		},
	}
}

// moduleDirs are the directories of the tree that hold .tf files.
func moduleDirs() []string {
	dirs := []string{"."}
	for i := range submodules {
		dirs = append(dirs, fmt.Sprintf("modules/sub%d", i))
	}
	for i := range examples {
		dirs = append(dirs, fmt.Sprintf("examples/ex%d", i))
	}
	return dirs
}

// treeInputs is the handoff's inputs of one tree: a SHA-256 per .tf file
// and tree_hash, the SHA-256 of one "<sha256>  <path>\n" line per file in
// path byte order. Every file of the tree is a regular .tf file under the
// size cap, so all of them count.
func treeInputs(files map[string]string) map[string]any {
	paths := slices.Sorted(func(yield func(string) bool) {
		for p := range files {
			if strings.HasSuffix(p, ".tf") && !yield(p) {
				return
			}
		}
	})
	h := sha256.New()
	list := make([]map[string]any, 0, len(paths))
	for _, p := range paths {
		sum := sha256.Sum256([]byte(files[p]))
		s := hex.EncodeToString(sum[:])
		fmt.Fprintf(h, "%s  %s\n", s, p)
		list = append(list, map[string]any{"path": p, "sha256": s})
	}
	return map[string]any{"tree_hash": hex.EncodeToString(h.Sum(nil)), "files": list}
}

// attrSet is the attributes of one block by name.
type attrSet map[string]any

// attrs returns the attributes names with the one flag kind set.
func attrs(kind string, names ...string) attrSet { return attrSet{}.with(kind, names...) }

func (a attrSet) with(kind string, names ...string) attrSet {
	for _, n := range names {
		a[n] = map[string]any{kind: true}
	}
	return a
}

// block returns a block with the attributes and the nested blocks.
func block(a attrSet, blocks map[string]any) map[string]any {
	b := map[string]any{"attributes": map[string]any(a)}
	if blocks != nil {
		b["block_types"] = blocks
	}
	return b
}

func typeSchema(a attrSet, blocks map[string]any) map[string]any {
	return map[string]any{"block": block(a, blocks)}
}

func nested(mode string, maxItems int, b map[string]any) map[string]any {
	n := map[string]any{"nesting_mode": mode, "block": b}
	if maxItems > 0 {
		n["max_items"] = maxItems
	}
	return n
}

// awsSchema is the aws provider at one version: the types the tree uses
// and genRes and genData unrelated ones. older drops what the older
// version lacks.
func awsSchema(version string, older bool, genRes, genData int) map[string]any {
	rule := attrs("optional", "description", "cidr_blocks", "ipv6_cidr_blocks", "prefix_list_ids", "security_groups", "self").
		with("required", "from_port", "to_port", "protocol")
	sg := attrs("optional", "name", "name_prefix", "description", "vpc_id", "revoke_rules_on_delete", "tags").
		with("computed", "id", "arn", "owner_id", "tags_all")
	if !older {
		sg.with("optional", "ingress_limit")
	}
	res := map[string]any{
		"aws_security_group": typeSchema(sg, map[string]any{
			"ingress":  nested("set", 0, block(rule, nil)),
			"egress":   nested("set", 0, block(attrSet{}.with("optional", "description", "cidr_blocks", "ipv6_cidr_blocks", "self").with("required", "from_port", "to_port", "protocol"), nil)),
			"timeouts": nested("single", 0, block(attrs("optional", "create", "delete"), nil)),
		}),
		"aws_iam_policy": typeSchema(attrs("optional", "name", "name_prefix", "description", "path", "tags").
			with("required", "policy").with("computed", "id", "arn", "policy_id", "tags_all"), nil),
		"aws_ssm_parameter": typeSchema(attrs("optional", "type", "value", "tags", "description", "tier", "overwrite").
			with("required", "name").with("computed", "id", "arn", "version", "tags_all"), nil),
	}
	data := map[string]any{
		"aws_partition":       typeSchema(attrs("computed", "id", "partition", "dns_suffix", "reverse_dns_prefix"), nil),
		"aws_caller_identity": typeSchema(attrs("computed", "id", "account_id", "arn", "user_id"), nil),
		"aws_iam_policy_document": typeSchema(attrs("optional", "version", "policy_id", "source_policy_documents").
			with("computed", "id", "json", "minified_json"), map[string]any{
			"statement": nested("list", 0, block(attrs("optional", "sid", "effect", "actions", "not_actions", "resources", "not_resources"), map[string]any{
				"condition":  nested("set", 0, block(attrs("required", "test", "variable", "values"), nil)),
				"principals": nested("set", 0, block(attrs("required", "type", "identifiers"), nil)),
			})),
		}),
	}
	for i := range genRes {
		res[fmt.Sprintf("aws_service%03d_resource%d", i/10, i%10)] = genType(i, older)
	}
	for i := range genData {
		data[fmt.Sprintf("aws_service%03d_lookup%d", i/10, i%10)] = genType(i, older)
	}
	return map[string]any{"source": awsSource, "version": version, "resource_schemas": res, "data_source_schemas": data}
}

// genType is unrelated type i. The older version lacks every fifth
// attribute.
func genType(i int, older bool) map[string]any {
	names := func(prefix string, n int) []string {
		var out []string
		for k := range n {
			if older && k%5 == 4 {
				continue
			}
			out = append(out, fmt.Sprintf("%s_%02d", prefix, k))
		}
		return out
	}
	top := attrs("optional", names("setting", genAttributes-4)...).
		with("required", "name").with("computed", "id", "arn", "tags_all")
	blocks := map[string]any{}
	for k := range genBlocks {
		inner := block(attrs("optional", names("value", genBlockAttributes/2)...), nil)
		blocks[fmt.Sprintf("configuration_%d", k)] = nested("list", i%3, block(
			attrs("optional", names("option", genBlockAttributes)...),
			map[string]any{"detail": nested("set", 0, inner)},
		))
	}
	return typeSchema(top, blocks)
}
