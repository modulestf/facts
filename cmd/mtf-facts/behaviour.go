package main

import (
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
	addTimeoutFlag(cmd, &timeout)
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
		timeout                               time.Duration
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
			return status(withTimeout(cmd, timeout, &lim, func() (any, error) {
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
	addTimeoutFlag(cmd, &timeout)
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

// withTimeout runs work under the timeout and writes its result as indented
// JSON. It returns exitClean or exitError.
func withTimeout(cmd *cobra.Command, timeout time.Duration, lim *facts.Limits, work func() (any, error)) int {
	return runBounded(cmd, timeout, lim, func() workResult {
		v, err := work()
		if err != nil {
			return workResult{err: err}
		}
		return jsonResult(v, exitClean)
	}, nil)
}
