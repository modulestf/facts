package main

import (
	"encoding/json"
	"errors"
	"fmt"
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
			return status(policyDocs(cmd, o, timeout))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.Base, "base", "", policyDocsBaseUsage)
	f.DurationVar(&timeout, "timeout", 60*time.Second, lintTimeoutUsage)
	return cmd
}

// policyDocs returns exitClean with the report written, or exitError with
// nothing on stdout.
func policyDocs(cmd *cobra.Command, o facts.PolicyDocsOptions, timeout time.Duration) int {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if timeout <= 0 {
		return usageError(cmd, timeoutNotPositive)
	}
	o.Limits.Deadline = time.Now().Add(timeout) // defense in depth; the timer below decides
	done := make(chan lintResult, 1)            // buffered: a late worker never blocks
	go func() { done <- policyDocsWork(o) }()
	timer := time.NewTimer(timeout)
	var res lintResult
	select {
	case res = <-done:
		timer.Stop()
	case <-timer.C:
		fmt.Fprint(stderr, "mtf-facts: timeout\n")
		return exitError
	}
	switch {
	case errors.Is(res.err, facts.ErrDeadline):
		fmt.Fprint(stderr, "mtf-facts: timeout\n")
		return exitError
	case res.err != nil:
		fmt.Fprintln(stderr, "mtf-facts:", res.err)
		return exitError
	}
	if _, err := stdout.Write(res.out); err != nil {
		return exitError
	}
	return exitClean
}

func policyDocsWork(o facts.PolicyDocsOptions) lintResult {
	r, err := facts.PolicyDocs(o)
	if err != nil {
		return lintResult{err: err}
	}
	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return lintResult{err: err}
	}
	out = append(out, '\n')
	if len(out) > o.Limits.MaxOutputBytes {
		return lintResult{err: fmt.Errorf("output over %d bytes", o.Limits.MaxOutputBytes)}
	}
	return lintResult{out: out}
}
