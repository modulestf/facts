package main

import (
	"strings"
	"time"

	"github.com/modulestf/facts/internal/depth"
	"github.com/modulestf/facts/internal/facts"
	"github.com/spf13/cobra"
)

// newDepthCmd builds depth, which decides how deep a review must go.
func newDepthCmd() *cobra.Command {
	var (
		o        = depth.Options{Limits: facts.DefaultLimits}
		examples string
		timeout  time.Duration
	)
	cmd := &cobra.Command{
		Use:     "depth",
		Short:   depthShort,
		Long:    depthLong,
		Example: depthExample,
		GroupID: groupPipeline,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.Head == "" || o.Base == "" || o.Files == "" {
				return status(usageError(cmd, depthRequired))
			}
			if examples != "" {
				o.Examples = strings.Split(examples, ",")
			}
			return status(depthRun(cmd, o, timeout))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.Head, "head", "", depthHeadUsage)
	f.StringVar(&o.Base, "base", "", depthBaseUsage)
	f.StringVar(&o.Files, "files", "", depthFilesUsage)
	f.StringVar(&o.Classify, "classify", "", depthClassifyUsage)
	f.StringVar(&o.Affected, "affected", "", depthAffectedUsage)
	f.StringVar(&examples, "examples", "", depthExamplesUsage)
	addTimeoutFlag(cmd, &timeout)
	return cmd
}

// depthRun runs depth under the timeout. A run past it is a deep report
// with deep-unwalked and exit 0, never an error: depth fails closed.
func depthRun(cmd *cobra.Command, o depth.Options, timeout time.Duration) int {
	return runBounded(cmd, timeout, &o.Limits, func() workResult {
		rep, err := depth.Run(o)
		if err != nil {
			return workResult{err: err}
		}
		return depthResult(rep)
	}, func() workResult { return depthResult(depth.Unwalked()) })
}

// depthResult is the output of rep and its notes.
func depthResult(rep *depth.Report) workResult {
	res := jsonResult(rep, exitClean)
	for _, n := range rep.Notes() {
		res.notes = append(res.notes, "mtf-facts: depth: "+n)
	}
	return res
}
