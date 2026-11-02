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
			return status(withTimeout(cmd, timeout, &o.Limits, func() (any, error) { return depth.Run(o) }))
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
	f.DurationVar(&timeout, "timeout", 60*time.Second, lintTimeoutUsage)
	return cmd
}
