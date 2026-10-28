package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/ledger"
	"github.com/spf13/cobra"
)

// Caps of the files ledger reads besides the tree and the schema files.
const (
	maxLedgerBytes   = 4 << 20
	maxRegistryBytes = 1 << 20
)

// ledgerReport is what ledger writes. Problems is null without --check and
// a list, empty when the ledger is complete, with it.
type ledgerReport struct {
	Envelope ledgerEnvelope   `json:"envelope"`
	Types    []ledger.Type    `json:"types"`
	Problems []ledger.Problem `json:"problems"`
}

// ledgerEnvelope binds the output to the binary, the registry, the schema
// files and the ledger, each file by its base name only.
type ledgerEnvelope struct {
	Tool           facts.Tool       `json:"tool"`
	RegistrySHA256 *string          `json:"registry_sha256"`
	SchemaFiles    []facts.FileHash `json:"schema_files"`
	Ledger         *facts.FileHash  `json:"ledger"`
}

// newLedgerCmd builds ledger, which lists the fields of the module in its
// argument.
func newLedgerCmd() *cobra.Command {
	var o ledgerOptions
	cmd := &cobra.Command{
		Use:     "ledger <dir>",
		Short:   ledgerShort,
		Long:    ledgerLong,
		Example: ledgerExample,
		GroupID: groupCheck,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return status(ledgerCmd(cmd, args[0], o))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringArrayVar(&o.schemaFiles, "schema-files", nil, ledgerSchemaFilesUsage)
	f.StringVar(&o.check, "check", "", ledgerCheckUsage)
	f.StringVar(&o.registry, "registry", "", ledgerRegistryUsage)
	return cmd
}

// ledgerOptions are the flags of ledger.
type ledgerOptions struct {
	schemaFiles     []string
	check, registry string
}

// ledgerCmd writes the field inventory of the module in dir as JSON.
// With --check it exits 1 when the ledger does not give every field exactly
// one disposition or names an input or output the module lacks.
func ledgerCmd(cmd *cobra.Command, dir string, o ledgerOptions) int {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	// A path flag given an empty value is refused, not read as absent.
	empty := ""
	switch f := cmd.Flags(); {
	case slices.Contains(o.schemaFiles, ""):
		empty = "schema-files"
	case f.Changed("check") && o.check == "":
		empty = "check"
	case f.Changed("registry") && o.registry == "":
		empty = "registry"
	}
	if empty != "" {
		fmt.Fprintf(stderr, "mtf-facts: --%s needs a path\n", empty)
		return exitError
	}
	rep, err := ledgerWork(dir, o.schemaFiles, o.check, o.registry)
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return exitError
	}
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return exitError
	}
	if _, err := stdout.Write(append(out, '\n')); err != nil {
		return exitError
	}
	if len(rep.Problems) > 0 {
		fmt.Fprintf(stderr, "mtf-facts: ledger: %d problems\n", len(rep.Problems))
		return exitFindings
	}
	return exitClean
}

func ledgerWork(dir string, schemaFiles []string, check, registry string) (*ledgerReport, error) {
	sum, err := binarySHA256()
	if err != nil {
		return nil, err
	}
	rep := &ledgerReport{Envelope: ledgerEnvelope{Tool: facts.Tool{Name: "mtf-facts", Version: version(), BinarySHA256: sum}}}
	if registry != "" {
		data, err := ledger.ReadFile(registry, maxRegistryBytes)
		if err != nil {
			return nil, fmt.Errorf("registry: %w", err)
		}
		h := hashOf(registry, data)
		rep.Envelope.RegistrySHA256 = &h.SHA256
	}
	lim := facts.DefaultLimits
	lim.Deadline = time.Now().Add(60 * time.Second)
	inv, err := ledger.Build(dir, schemaFiles, lim)
	if err != nil {
		return nil, err
	}
	rep.Types = inv.Types
	rep.Envelope.SchemaFiles = []facts.FileHash{}
	for _, f := range inv.SchemaFiles {
		rep.Envelope.SchemaFiles = append(rep.Envelope.SchemaFiles, facts.FileHash{Path: f.Path, SHA256: f.SHA256})
	}
	if check == "" {
		return rep, nil
	}
	data, err := ledger.ReadFile(check, maxLedgerBytes)
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}
	h := hashOf(check, data)
	rep.Envelope.Ledger = &h
	l, err := ledger.Parse(data)
	if err != nil {
		return nil, err
	}
	rep.Problems = ledger.Check(inv, l)
	return rep, nil
}

// hashOf records a file by its base name, so no host path reaches the
// output. The bytes are hashed as read: a CRLF and an LF copy differ.
func hashOf(p string, data []byte) facts.FileHash {
	s := sha256.Sum256(data)
	return facts.FileHash{Path: filepath.Base(p), SHA256: hex.EncodeToString(s[:])}
}
