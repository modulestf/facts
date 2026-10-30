package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/spf13/cobra"
)

// newAffectedCmd builds affected, which lists the examples a change can
// affect.
func newAffectedCmd() *cobra.Command {
	var (
		o       = facts.AffectedOptions{Limits: facts.DefaultLimits}
		asJSON  bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:     "affected <dir>",
		Short:   affectedShort,
		Long:    affectedLong,
		Example: affectedExample,
		GroupID: groupPipeline,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Head = args[0]
			return status(affected(cmd, o, asJSON, timeout))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.Base, "base", "", affectedBaseUsage)
	f.StringVar(&o.Diff, "diff", "", affectedDiffUsage)
	f.BoolVar(&asJSON, "json", false, affectedJSONUsage)
	f.DurationVar(&timeout, "timeout", 60*time.Second, lintTimeoutUsage)
	cmd.MarkFlagsMutuallyExclusive("base", "diff")
	return cmd
}

// affected returns exitClean or exitError, as lint does with its timeout.
func affected(cmd *cobra.Command, o facts.AffectedOptions, asJSON bool, timeout time.Duration) int {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if timeout <= 0 {
		return usageError(cmd, timeoutNotPositive)
	}
	o.Limits.Deadline = time.Now().Add(timeout) // defense in depth; the timer below decides
	done := make(chan lintResult, 1)            // buffered: a late worker never blocks
	go func() { done <- affectedWork(o, asJSON) }()
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

func affectedWork(o facts.AffectedOptions, asJSON bool) lintResult {
	r, err := facts.Affected(o)
	if err != nil {
		return lintResult{err: err}
	}
	var out []byte
	if asJSON {
		if out, err = json.MarshalIndent(r, "", "  "); err != nil {
			return lintResult{err: err}
		}
		out = append(out, '\n')
	} else {
		var b strings.Builder
		for _, e := range r.Examples {
			fmt.Fprintf(&b, "%s %s\n", e.Reason, e.Dir)
		}
		fb := "none"
		if r.Fallback != nil {
			fb = *r.Fallback
		}
		fmt.Fprintf(&b, "inert: %v, fallback: %s, %d changed paths\n", r.Inert, fb, len(r.Changed))
		out = []byte(b.String())
	}
	if len(out) > o.Limits.MaxOutputBytes {
		return lintResult{err: fmt.Errorf("output over %d bytes", o.Limits.MaxOutputBytes)}
	}
	return lintResult{out: out}
}
