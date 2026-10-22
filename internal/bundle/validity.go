package bundle

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/profile"
	"github.com/modulestf/facts/internal/schema"
)

// ErrInvalid marks a report the validity predicate refuses.
var ErrInvalid = errors.New("bundle invalid")

// Pins are what the workflow expects a report to be bound to: the workspace
// HEAD, the recorded merge base, the pinned binary digest, the hash of the
// rule registry at the pinned skills commit, the schema input the run was
// given and the profile's SHA-256. A nil Schema or ProfileSHA256 expects the
// report to have read none.
type Pins struct {
	HeadSHA        string
	MergeBaseSHA   string
	BinarySHA256   string
	RegistrySHA256 string
	Schema         *facts.SchemaInputs
	ProfileSHA256  *string
}

// Check is the validity predicate, the one place it is implemented. Every
// field of the envelope must equal its pin. The error names each field that
// does not and wraps ErrInvalid.
func Check(env facts.Envelope, p Pins) error {
	var errs []error
	bad := func(field string, got, want any) {
		errs = append(errs, fmt.Errorf("%s is %v, want %v", field, got, want))
	}
	if env.SchemaVersion != facts.SchemaVersion {
		bad("schema_version", env.SchemaVersion, facts.SchemaVersion)
	}
	if !equalPtr(env.HeadSHA, &p.HeadSHA) {
		bad("head_sha", show(env.HeadSHA), p.HeadSHA)
	}
	if !equalPtr(env.MergeBaseSHA, &p.MergeBaseSHA) {
		bad("merge_base_sha", show(env.MergeBaseSHA), p.MergeBaseSHA)
	}
	if env.Tool.BinarySHA256 != p.BinarySHA256 {
		bad("tool.binary_sha256", env.Tool.BinarySHA256, p.BinarySHA256)
	}
	if !equalPtr(env.RegistrySHA256, &p.RegistrySHA256) {
		bad("registry_sha256", show(env.RegistrySHA256), p.RegistrySHA256)
	}
	if !equalPtr(env.ProfileSHA256, p.ProfileSHA256) {
		bad("profile_sha256", show(env.ProfileSHA256), show(p.ProfileSHA256))
	}
	errs = append(errs, checkSchema(env.SchemaInputs, p.Schema)...)
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
}

func checkSchema(got, want *facts.SchemaInputs) []error {
	switch {
	case got == nil && want == nil:
		return nil
	case got == nil || want == nil:
		return []error{fmt.Errorf("schema_inputs present is %v, want %v", got != nil, want != nil)}
	}
	var errs []error
	for _, f := range []struct {
		name      string
		got, want *string
	}{
		{"schema_inputs.handoff_sha256", got.HandoffSHA256, want.HandoffSHA256},
		{"schema_inputs.head_sha", got.HeadSHA, want.HeadSHA},
		{"schema_inputs.merge_base_sha", got.MergeBaseSHA, want.MergeBaseSHA},
	} {
		if !equalPtr(f.got, f.want) {
			errs = append(errs, fmt.Errorf("%s is %s, want %s", f.name, show(f.got), show(f.want)))
		}
	}
	if !equalPtr(got.TreeHashes, want.TreeHashes) {
		errs = append(errs, errors.New("schema_inputs.tree_hashes differ"))
	}
	if !slices.Equal(got.SchemaFiles, want.SchemaFiles) {
		errs = append(errs, errors.New("schema_inputs.schema_files differ"))
	}
	return errs
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func show(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

// SchemaPins loads the schema input the workflow gave the lint run, with the
// loader lint uses, and returns what its envelope must record; nil when the
// run was given none.
func SchemaPins(handoff string, files []string) (*facts.SchemaInputs, error) {
	if handoff == "" && len(files) == 0 {
		return nil, nil
	}
	s, err := schema.Load(handoff, files)
	if err != nil {
		return nil, err
	}
	in := &facts.SchemaInputs{SchemaFiles: []facts.FileHash{}}
	for _, f := range s.SchemaFiles {
		in.SchemaFiles = append(in.SchemaFiles, facts.FileHash{Path: f.Path, SHA256: f.SHA256})
	}
	if h := s.Handoff; h != nil {
		sum, head, mergeBase := s.HandoffSHA256, h.HeadSHA, h.MergeBaseSHA
		in.HandoffSHA256, in.HeadSHA, in.MergeBaseSHA = &sum, &head, &mergeBase
		in.TreeHashes = &facts.TreeHashes{Head: h.Inputs.Head.TreeHash, MergeBase: h.Inputs.MergeBase.TreeHash}
	}
	return in, nil
}

// profileLimits are the limits lint reads a profile directory with.
var profileLimits = facts.Limits{MaxFiles: 1000, MaxFileBytes: 256 << 10, MaxTotalBytes: 4 << 20}

// ProfileSHA256 returns the profile_sha256 a lint run given dir records: the
// profile's SHA-256, or nil when the directory holds no profile that reads
// and parses. A dir that is not a directory is an error, as it is to lint.
func ProfileSHA256(dir string) (*string, error) {
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("profile: %s is not a directory", dir)
	}
	t, err := facts.ReadTree(dir, profileLimits)
	if err != nil {
		return nil, nil // lint records no profile for a directory it cannot read
	}
	files := map[string][]byte{}
	for _, e := range t.Entries {
		if e.Kind == facts.KindFile {
			files[e.Path] = e.Data
		}
	}
	p, err := profile.New(files)
	if err != nil {
		return nil, nil // lint records no profile for one that does not parse
	}
	return &p.SHA256, nil
}
