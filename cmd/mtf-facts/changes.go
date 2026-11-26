package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/spf13/cobra"
)

// newChangesCmd builds changes, which reports the changed attributes of a
// change and what reads them.
func newChangesCmd() *cobra.Command {
	var (
		o       = facts.ChangesOptions{Limits: facts.DefaultLimits}
		asJSON  bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:     "changes <dir>",
		Short:   changesShort,
		Long:    changesLong,
		Example: changesExample,
		GroupID: groupPipeline,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Head = args[0]
			if (o.Base == "") == (o.Diff == "") {
				return status(usageError(cmd, changesBaseRequired))
			}
			return status(runBounded(cmd, timeout, &o.Limits, func() workResult { return changesWork(o, asJSON) }, nil))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.Base, "base", "", affectedBaseUsage)
	f.StringVar(&o.Diff, "diff", "", affectedDiffUsage)
	f.BoolVar(&asJSON, "json", false, changesJSONUsage)
	addTimeoutFlag(cmd, &timeout)
	cmd.MarkFlagsMutuallyExclusive("base", "diff")
	return cmd
}

func changesWork(o facts.ChangesOptions, asJSON bool) workResult {
	r, err := facts.Changes(o)
	if err != nil {
		return workResult{err: err}
	}
	if asJSON {
		return jsonResult(r, exitClean)
	}
	return workResult{out: []byte(changesText(r)), code: exitClean}
}

// changesText is the text form of a changes report, one line per file,
// block, attribute and directory with data sources.
func changesText(r *facts.ChangesReport) string {
	var b strings.Builder
	for _, f := range r.Files {
		parse := "parsed"
		if f.Reason != nil {
			parse = "not parsed: " + *f.Reason
		}
		fmt.Fprintf(&b, "%s %s, %s, outside %v\n", f.Path, f.Status, parse, f.Outside)
		for _, bl := range f.Blocks {
			fmt.Fprintf(&b, "  %s %s, structural %v\n", bl.Address, bl.Status, bl.Structural)
			for _, a := range bl.Attributes {
				fmt.Fprintf(&b, "    %s plain %v/%v, %s\n", a.Name, a.Plain.Head, a.Plain.Base, a.Read)
			}
		}
	}
	for _, d := range r.DataSources {
		if len(d.Types) > 0 || !d.Complete {
			fmt.Fprintf(&b, "data %s: %s, complete %v\n", d.Dir, strings.Join(d.Types, " "), d.Complete)
		}
	}
	return b.String()
}
