package main

import (
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/spf13/cobra"
)

// newPolicyDocsCmd builds iam-policy-docs, which reports the IAM policy
// documents of a module tree and the blocks that use them.
func newPolicyDocsCmd() *cobra.Command {
	var (
		o       = facts.PolicyDocsOptions{Limits: facts.DefaultLimits}
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:     "iam-policy-docs <dir>",
		Short:   policyDocsShort,
		Long:    policyDocsLong,
		Example: policyDocsExample,
		GroupID: groupPipeline,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Head = args[0]
			return status(runBounded(cmd, timeout, &o.Limits, func() workResult { return policyDocsWork(o) }, nil))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.Base, "base", "", policyDocsBaseUsage)
	addTimeoutFlag(cmd, &timeout)
	return cmd
}

func policyDocsWork(o facts.PolicyDocsOptions) workResult {
	r, err := facts.PolicyDocs(o)
	if err != nil {
		return workResult{err: err}
	}
	return jsonResult(r, exitClean)
}
