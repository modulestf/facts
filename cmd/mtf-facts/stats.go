package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/modulestf/facts/internal/stats"
)

// statsFileUsage is the help of --stats-file, which every command a review
// runs accepts.
const statsFileUsage = "also write where the run spent its time and memory, as JSON, to this `file`; " +
	"stdout and the exit code stay the same, and a file that cannot be written is one line on stderr " +
	"(put it outside every input tree; an existing file must be a regular file you own; a link, an input " +
	"file and the file stdout or stderr goes to are refused)"

// addStatsFlag adds --stats-file to cmd, bound to p.
func addStatsFlag(cmd *cobra.Command, p *string) {
	cmd.Flags().StringVar(p, "stats-file", "", statsFileUsage)
}

// withStats runs handler with a recorder when path is set, and nil
// otherwise, then writes the record and returns the handler's exit code
// as RunE does. The record is written after the handler has written its
// output, so it never changes stdout or the exit code: a record that cannot
// be written is one line on stderr.
func withStats(cmd *cobra.Command, args []string, path string, handler func(rec *stats.Recorder) int) error {
	var rec *stats.Recorder
	if path != "" {
		rec = stats.New()
	}
	code := handler(rec)
	if path == "" && cmd.Flags().Changed("stats-file") {
		fmt.Fprintln(cmd.ErrOrStderr(), "mtf-facts: stats: --stats-file is empty, no record written")
	}
	if rec != nil {
		if err := writeStats(path, rec.Record(cmd.Name(), version(), code), statsGuard(cmd, args)); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "mtf-facts: stats: %s\n", oneLine(err.Error()))
		}
	}
	return status(code)
}

// writeStats writes r to path under g.
func writeStats(path string, r *stats.Record, g stats.Guard) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return stats.WriteFile(path, append(b, '\n'), g)
}

// statsGuard names what the record of cmd must not overwrite: the files
// stdout and stderr go to, and every input. Inputs are the arguments and
// the values of every string flag set, --stats-file aside: each that names
// a directory is an input tree, and each that names a regular file is an
// input file. So a file flag added later is guarded with no change here; a
// value that names no path is left out.
func statsGuard(cmd *cobra.Command, args []string) stats.Guard {
	g := stats.Guard{Outputs: []*os.File{os.Stdout, os.Stderr}}
	add := func(v string) {
		if v == "" {
			return
		}
		switch st, err := os.Stat(v); {
		case err != nil:
		case st.IsDir():
			g.Trees = append(g.Trees, v)
		case st.Mode().IsRegular():
			g.Files = append(g.Files, v)
		}
	}
	for _, a := range args {
		add(a)
	}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if f.Name == "stats-file" {
			return
		}
		switch f.Value.Type() {
		case "string":
			add(f.Value.String())
		case "stringArray", "stringSlice":
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				for _, v := range sv.GetSlice() {
					add(v)
				}
			}
		}
	})
	return g
}
