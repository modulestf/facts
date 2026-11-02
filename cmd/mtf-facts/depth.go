package main

import (
	"encoding/json"
	"fmt"
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
	f.DurationVar(&timeout, "timeout", 60*time.Second, lintTimeoutUsage)
	return cmd
}

// depthRun runs depth under the timeout. A run past it is a deep report
// with deep-unwalked and exit 0, never an error: depth fails closed.
func depthRun(cmd *cobra.Command, o depth.Options, timeout time.Duration) int {
	if timeout <= 0 {
		return usageError(cmd, timeoutNotPositive)
	}
	o.Limits.Deadline = time.Now().Add(timeout)
	type result struct {
		rep *depth.Report
		err error
	}
	done := make(chan result, 1) // buffered: a late worker never blocks
	go func() {
		rep, err := depth.Run(o)
		done <- result{rep, err}
	}()
	timer := time.NewTimer(timeout)
	var res result
	select {
	case res = <-done:
		timer.Stop()
	case <-timer.C:
		res.rep = depth.Unwalked() // main's os.Exit ends the worker
	}
	if res.err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "mtf-facts:", res.err)
		return exitError
	}
	out, err := json.MarshalIndent(res.rep, "", "  ")
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "mtf-facts:", err)
		return exitError
	}
	if _, err := cmd.OutOrStdout().Write(append(out, '\n')); err != nil {
		return exitError
	}
	return exitClean
}
