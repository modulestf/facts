package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modulestf/facts/internal/behaviour"
	"github.com/modulestf/facts/internal/facts"
	"github.com/spf13/cobra"
)

// newBehaviourCmd builds behaviour, which classifies a change into triggers.
func newBehaviourCmd() *cobra.Command {
	var (
		o       = behaviour.Options{Limits: facts.DefaultLimits}
		asJSON  bool
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:     "behaviour <dir>",
		Short:   behaviourShort,
		Long:    behaviourLong,
		Example: behaviourExample,
		GroupID: groupPipeline,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.Head = args[0]
			if !asJSON {
				return status(usageError(cmd, jsonRequired))
			}
			if (o.Base == "") == (o.Diff == "") {
				return status(usageError(cmd, behaviourChangeRequired))
			}
			return status(withTimeout(cmd, timeout, &o.Limits, func() (any, error) { return behaviour.Run(o) }))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.Base, "base", "", behaviourBaseUsage)
	f.StringVar(&o.Diff, "diff", "", behaviourDiffUsage)
	f.StringVar(&o.Schema, "schema", "", behaviourSchemaUsage)
	f.BoolVar(&asJSON, "json", false, jsonUsage)
	f.DurationVar(&timeout, "timeout", 60*time.Second, lintTimeoutUsage)
	cmd.MarkFlagsMutuallyExclusive("base", "diff")
	return cmd
}

// newClaimsEvalCmd builds claims-eval, which validates the claims and
// computes the state of every trigger and claim from a ladder record.
func newClaimsEvalCmd() *cobra.Command {
	var (
		behaviourFile, ladderFile, claimsFile string
		headSHA, baseSHA                      string
		asJSON                                bool
	)
	cmd := &cobra.Command{
		Use:     "claims-eval",
		Short:   claimsEvalShort,
		Long:    claimsEvalLong,
		Example: claimsEvalExample,
		GroupID: groupPipeline,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !asJSON {
				return status(usageError(cmd, jsonRequired))
			}
			if behaviourFile == "" || ladderFile == "" {
				return status(usageError(cmd, claimsEvalRequired))
			}
			lim := facts.DefaultLimits
			return status(withTimeout(cmd, 60*time.Second, &lim, func() (any, error) {
				return claimsEval(behaviourFile, ladderFile, claimsFile, headSHA, baseSHA)
			}))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&behaviourFile, "behaviour", "", claimsEvalBehaviourUsage)
	f.StringVar(&ladderFile, "ladder", "", claimsEvalLadderUsage)
	f.StringVar(&claimsFile, "claims", "", claimsEvalClaimsUsage)
	f.StringVar(&headSHA, "head-sha", "", claimsEvalHeadSHAUsage)
	f.StringVar(&baseSHA, "base-sha", "", claimsEvalBaseSHAUsage)
	f.BoolVar(&asJSON, "json", false, jsonUsage)
	return cmd
}

func claimsEval(behaviourFile, ladderFile, claimsFile, headSHA, baseSHA string) (*behaviour.EvalReport, error) {
	b, err := behaviour.ReadBehaviour(behaviourFile)
	if err != nil {
		return nil, err
	}
	l, err := behaviour.ReadLadder(ladderFile)
	if err != nil {
		return nil, err
	}
	if err := l.Bind(headSHA, baseSHA); err != nil {
		return nil, err
	}
	var (
		claims    []behaviour.Claim
		discarded []behaviour.Discarded
	)
	if claimsFile != "" {
		if claims, discarded, err = behaviour.ReadClaims(claimsFile, b); err != nil {
			return nil, err
		}
	}
	return behaviour.Evaluate(b, l, claims, discarded), nil
}

// withTimeout runs work in a worker under a deadline and writes its result
// as indented JSON. It returns exitClean or exitError.
func withTimeout(cmd *cobra.Command, timeout time.Duration, lim *facts.Limits, work func() (any, error)) int {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if timeout <= 0 {
		return usageError(cmd, timeoutNotPositive)
	}
	// Defense in depth only: the timer below is the timeout mechanism.
	lim.Deadline = time.Now().Add(timeout)
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1) // buffered: a late worker never blocks
	go func() {
		v, err := work()
		if err != nil {
			done <- result{err: err}
			return
		}
		out, err := json.MarshalIndent(v, "", "  ")
		done <- result{out: append(out, '\n'), err: err}
	}()
	timer := time.NewTimer(timeout)
	var res result
	select {
	case res = <-done:
		timer.Stop()
	case <-timer.C:
		// main's os.Exit ends the worker, which may be stuck in a parser.
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
	case len(res.out) > lim.MaxOutputBytes:
		fmt.Fprintf(stderr, "mtf-facts: output over %d bytes\n", lim.MaxOutputBytes)
		return exitError
	}
	if _, err := stdout.Write(res.out); err != nil {
		return exitError
	}
	return exitClean
}
