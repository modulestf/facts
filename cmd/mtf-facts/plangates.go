package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/gates"
	"github.com/modulestf/facts/internal/stats"
	"github.com/spf13/cobra"
)

// planGatesStdinBytes caps the file list read from standard input.
const planGatesStdinBytes = 8 << 20

// planGatesOptions are the flags of plan-gates.
type planGatesOptions struct {
	scope, mode, root, example string
	providers                  []string
}

// newPlanGatesCmd builds plan-gates.
func newPlanGatesCmd() *cobra.Command {
	var (
		o       planGatesOptions
		timeout time.Duration
		statsTo string
	)
	cmd := &cobra.Command{
		Use:     "plan-gates --scope <clone|downloaded> --mode <laptop|runner> --root <dir>",
		Short:   planGatesShort,
		Long:    planGatesLong,
		Example: planGatesExample,
		GroupID: groupPipeline,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withStats(cmd, args, statsTo, func(rec *stats.Recorder) int {
				return planGates(cmd, o, timeout, rec)
			})
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&o.scope, "scope", "", planGatesScopeUsage)
	f.StringVar(&o.mode, "mode", "", planGatesModeUsage)
	f.StringVar(&o.root, "root", "", planGatesRootUsage)
	f.StringVar(&o.example, "example", "", planGatesExampleUsage)
	f.StringArrayVar(&o.providers, "provider", nil, planGatesProviderUsage)
	addTimeoutFlag(cmd, &timeout)
	addStatsFlag(cmd, &statsTo)
	return cmd
}

// planGates runs the gates over the files on standard input. It exits 0
// with no hit, 1 with hits and 2 on a usage error, bad input, the output
// cap or the timeout.
func planGates(cmd *cobra.Command, o planGatesOptions, timeout time.Duration, rec *stats.Recorder) int {
	if o.scope != "clone" && o.scope != "downloaded" {
		return usageError(cmd, "--scope must be clone or downloaded")
	}
	if o.mode != "laptop" && o.mode != "runner" {
		return usageError(cmd, "--mode must be laptop or runner")
	}
	if o.root == "" {
		return usageError(cmd, "--root is required")
	}
	if err := gates.CheckProviders(o.providers); err != nil {
		return usageError(cmd, err.Error())
	}
	in := cmd.InOrStdin()
	lim := facts.DefaultLimits
	lim.Stats = rec
	return runBounded(cmd, timeout, &lim, func() workResult {
		rec.Phase("read")
		paths, err := readPathList(in, lim.MaxFiles)
		if err != nil {
			return workResult{err: fmt.Errorf("plan-gates: %w", err)}
		}
		rec.Phase("gates")
		out, err := gates.Run(paths, gates.Options{
			Downloaded: o.scope == "downloaded",
			Runner:     o.mode == "runner",
			Root:       o.root,
			Example:    o.example,
			Providers:  o.providers,
			FileBytes:  lim.MaxFileBytes,
			TotalBytes: lim.MaxTotalBytes,
			Deadline:   lim.Deadline,
		})
		if errors.Is(err, gates.ErrDeadline) {
			return workResult{err: facts.ErrDeadline}
		}
		if err != nil {
			return workResult{err: fmt.Errorf("plan-gates: %w", err)}
		}
		code := exitClean
		if len(out) > 0 {
			code = exitFindings
		}
		return workResult{out: out, code: code}
	}, nil)
}

// readPathList reads one path a line, as Perl's <STDIN> with chomp does:
// a last line with no newline counts, and an empty input is no file.
func readPathList(in io.Reader, maxFiles int) ([]string, error) {
	data, err := io.ReadAll(io.LimitReader(in, planGatesStdinBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > planGatesStdinBytes {
		return nil, fmt.Errorf("file list over %d bytes", planGatesStdinBytes)
	}
	if len(data) == 0 {
		return nil, nil
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) > maxFiles {
		return nil, fmt.Errorf("file list over %d files", maxFiles)
	}
	return lines, nil
}
