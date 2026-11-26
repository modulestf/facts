package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/modulestf/facts/internal/facts"
	factsmerge "github.com/modulestf/facts/internal/merge"
	"github.com/modulestf/facts/internal/readcap"
	"github.com/modulestf/facts/internal/registry"
	"github.com/spf13/cobra"
)

// Exit codes of merge check, beside exitError.
const (
	exitReplyValid  = 0
	exitReplyRepair = 1
)

// maxRegistryFileBytes bounds the rule-facts.md the merge reads.
const maxRegistryFileBytes = 1 << 20

// newMergeCmd builds merge, whose subcommands check a lead reply or merge it
// with the facts into the final findings.
func newMergeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "merge <command>",
		Short:   mergeShort,
		Long:    mergeLong,
		GroupID: groupPipeline,
		Args:    cobra.NoArgs,
		RunE:    usageExit,
	}
	cmd.AddCommand(newMergeCheckCmd(), newMergeFinalCmd(), newMergeCountsCmd())
	return cmd
}

// mergeRun runs a merge subcommand's work under the timeout.
func mergeRun(cmd *cobra.Command, timeout time.Duration, work func() workResult) int {
	lim := facts.DefaultLimits
	return runBounded(cmd, timeout, &lim, work, nil)
}

func newMergeCheckCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:     "check <bundle> <reply>",
		Short:   mergeCheckShort,
		Long:    mergeCheckLong,
		Example: mergeCheckExample,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return status(mergeRun(cmd, timeout, func() workResult { return mergeCheck(args) }))
		},
	}
	addTimeoutFlag(cmd, &timeout)
	return cmd
}

func newMergeCountsCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:     "counts <bundle> <reply> [<repair-reply>]",
		Short:   mergeCountsShort,
		Long:    mergeCountsLong,
		Example: mergeCountsExample,
		Args:    cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return status(mergeRun(cmd, timeout, func() workResult {
				out, err := mergeCountsWork(args)
				return workResult{out: append(out, '\n'), err: err}
			}))
		},
	}
	addTimeoutFlag(cmd, &timeout)
	return cmd
}

func newMergeFinalCmd() *cobra.Command {
	var (
		regFile, reportFile, templatesFile string
		timeout                            time.Duration
	)
	cmd := &cobra.Command{
		Use:     "final <bundle> <reply> [<repair-reply>]",
		Short:   mergeFinalShort,
		Long:    mergeFinalLong,
		Example: mergeFinalExample,
		Args:    cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if regFile == "" {
				return status(usageError(cmd, registryRequired))
			}
			return status(mergeRun(cmd, timeout, func() workResult { return mergeFinal(regFile, reportFile, templatesFile, args) }))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&regFile, "registry", "", mergeFinalRegistryUsage)
	f.StringVar(&reportFile, "report", "", mergeFinalReportUsage)
	f.StringVar(&templatesFile, "templates", "", mergeFinalTemplatesUsage)
	addTimeoutFlag(cmd, &timeout)
	return cmd
}

// checkResult is what merge check prints.
type checkResult struct {
	Result  string   `json:"result"`
	Reasons []string `json:"reasons,omitempty"`
}

func mergeCheck(args []string) workResult {
	b, err := mergeBundle(args[0])
	if err != nil {
		return workResult{err: err}
	}
	reply, err := mergeReadFile(args[1], factsmerge.MaxReplyBytes)
	if err != nil {
		return workResult{err: err}
	}
	res, code := checkResult{Result: "valid"}, exitReplyValid
	if reasons := factsmerge.Problems(b, factsmerge.ParseReply(reply)); len(reasons) > 0 {
		res, code = checkResult{Result: "repair", Reasons: reasons}, exitReplyRepair
	}
	out, err := json.Marshal(res)
	if err != nil {
		return workResult{err: err}
	}
	return workResult{out: append(out, '\n'), code: code}
}

func mergeCountsWork(files []string) ([]byte, error) {
	b, err := mergeBundle(files[0])
	if err != nil {
		return nil, err
	}
	first, err := mergeReadFile(files[1], factsmerge.MaxReplyBytes)
	if err != nil {
		return nil, err
	}
	var repair []byte
	if len(files) == 3 {
		if repair, err = mergeReadFile(files[2], factsmerge.MaxReplyBytes); err != nil {
			return nil, err
		}
	}
	c, err := factsmerge.ReplyCounts(b, first, repair)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Counts *factsmerge.Counts `json:"counts"`
	}{c})
}

// mergeFinal merges the findings from the files: the bundle, the first
// reply and the repair reply, if given.
func mergeFinal(regFile, reportFile, templatesFile string, files []string) workResult {
	out, stats, err := mergeFinalWork(regFile, reportFile, templatesFile, files)
	if err != nil {
		return workResult{err: err}
	}
	res := workResult{out: out}
	if note := offListNote(stats.OffList); note != "" {
		res.notes = []string{note}
	}
	return res
}

// offListNote is the stderr line for n model severities off their rule's
// list, or "" for none.
func offListNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("mtf-facts: merge: %d finding severities were off their rule's list and dropped for the nearest permitted one", n)
}

// mergeFinalWork reads the inputs and returns the merged findings as JSON,
// with the merge's counts.
func mergeFinalWork(regFile, reportFile, templatesFile string, files []string) ([]byte, factsmerge.Stats, error) {
	in, first, repair, err := mergeFinalInputs(regFile, reportFile, templatesFile, files)
	if err != nil {
		return nil, factsmerge.Stats{}, err
	}
	findings, stats, err := factsmerge.FinalStats(in, first, repair)
	if err != nil {
		return nil, stats, err
	}
	out, err := factsmerge.Write(findings)
	return out, stats, err
}

// mergeFinalInputs reads the registry, the bundle, the optional report and
// templates, and the replies.
func mergeFinalInputs(regFile, reportFile, templatesFile string, files []string) (in factsmerge.Inputs, first, repair []byte, err error) {
	var data []byte
	data, err = mergeReadFile(regFile, maxRegistryFileBytes)
	if err != nil {
		return in, nil, nil, err
	}
	if len(data) > maxRegistryFileBytes {
		return in, nil, nil, fmt.Errorf("registry is larger than %d bytes", maxRegistryFileBytes)
	}
	if in.Registry, err = registry.Parse(string(data)); err != nil {
		return in, nil, nil, fmt.Errorf("registry: %w", err)
	}
	if in.Bundle, err = mergeBundle(files[0]); err != nil {
		return in, nil, nil, err
	}
	if reportFile != "" {
		if data, err = mergeReadFile(reportFile, factsmerge.MaxReportBytes); err != nil {
			return in, nil, nil, err
		}
		if in.Index, err = factsmerge.ParseIndex(data); err != nil {
			return in, nil, nil, err
		}
	}
	if templatesFile != "" {
		if data, err = mergeReadFile(templatesFile, factsmerge.MaxTemplateBytes); err != nil {
			return in, nil, nil, err
		}
		if in.Templates, err = factsmerge.ParseTemplates(data); err != nil {
			return in, nil, nil, err
		}
	}
	first, err = mergeReadFile(files[1], factsmerge.MaxReplyBytes)
	if err != nil {
		return in, nil, nil, err
	}
	if len(files) == 3 {
		if repair, err = mergeReadFile(files[2], factsmerge.MaxReplyBytes); err != nil {
			return in, nil, nil, err
		}
	}
	return in, first, repair, nil
}

func mergeBundle(path string) (*factsmerge.Bundle, error) {
	data, err := mergeReadFile(path, factsmerge.MaxBundleBytes)
	if err != nil {
		return nil, err
	}
	return factsmerge.ParseBundle(data)
}

// mergeReadFile reads a regular file, not a link, of at most limit bytes,
// plus one byte more when it is larger, so a reply over its cap is read as
// too large rather than cut. It never returns nil data without an error.
func mergeReadFile(path string, limit int) ([]byte, error) {
	data, err := readcap.Prefix(path, int64(limit))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return data, nil
}
