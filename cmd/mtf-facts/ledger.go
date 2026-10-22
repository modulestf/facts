package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/ledger"
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

// ledgerCmd writes the field inventory of the module in a directory as JSON.
// With --check it exits 1 when the ledger does not give every field exactly
// one disposition or names an input or output the module lacks.
func ledgerCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ledger", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var schemaFiles []string
	fs.Func("schema-files", "a terraform providers schema -json file; repeat for more", func(p string) error {
		if p == "" {
			return errors.New("empty path")
		}
		schemaFiles = append(schemaFiles, p)
		return nil
	})
	check := fs.String("check", "", "coverage ledger JSON file to check against the inventory")
	registry := fs.String("registry", "", "rule-facts.md to hash into the output")
	var dirs []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return exitError
		}
		if fs.NArg() == 0 {
			break
		}
		dirs, rest = append(dirs, fs.Arg(0)), fs.Args()[1:]
	}
	if len(dirs) != 1 {
		fmt.Fprint(stderr, usage)
		return exitError
	}
	// A path flag given an empty value is refused, not read as absent.
	// --schema-files refuses one itself.
	empty := ""
	fs.Visit(func(f *flag.Flag) {
		if (f.Name == "check" || f.Name == "registry") && f.Value.String() == "" {
			empty = f.Name
		}
	})
	if empty != "" {
		fmt.Fprintf(stderr, "mtf-facts: --%s needs a path\n", empty)
		return exitError
	}
	rep, err := ledgerWork(dirs[0], schemaFiles, *check, *registry)
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
