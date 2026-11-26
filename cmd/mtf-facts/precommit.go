package main

import (
	"errors"
	"io/fs"
	"os"
	"regexp"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/readcap"
	"github.com/spf13/cobra"
)

// exitUndefined is the exit code of pre-commit derive when derive is
// undefined for its input.
const exitUndefined = 3

// newPreCommitCmd builds pre-commit, whose subcommands read what
// pre-commit-terraform's hooks write and pin.
func newPreCommitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pre-commit <command>",
		Short:   preCommitShort,
		Long:    preCommitLong,
		GroupID: groupPipeline,
		Args:    cobra.NoArgs,
		RunE:    usageExit,
	}
	cmd.AddCommand(newPreCommitDeriveCmd(), newPreCommitWrapperCmd(), newPreCommitDocsCmd())
	return cmd
}

// preCommitRun runs a pre-commit subcommand's work under the timeout.
func preCommitRun(cmd *cobra.Command, timeout time.Duration, work func() workResult) int {
	lim := facts.DefaultLimits
	return runBounded(cmd, timeout, &lim, work, nil)
}

func newPreCommitDeriveCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:     "derive <versions.tf>",
		Short:   preCommitDeriveShort,
		Long:    preCommitDeriveLong,
		Example: preCommitDeriveExample,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return status(preCommitRun(cmd, timeout, func() workResult {
				// Any file it cannot read, as an oversized one, has no derive.
				data, err := readcap.File(args[0], facts.MaxWrapperFileBytes)
				if err != nil {
					return workResult{code: exitUndefined}
				}
				d, ok := facts.WrapperDerive(data)
				if !ok {
					return workResult{code: exitUndefined}
				}
				return workResult{out: d, code: exitClean}
			}))
		},
	}
	addTimeoutFlag(cmd, &timeout)
	return cmd
}

func newPreCommitWrapperCmd() *cobra.Command {
	var (
		config, module, wrapper string
		timeout                 time.Duration
	)
	cmd := &cobra.Command{
		Use:     "wrapper --config <file> --module <file> --wrapper <file>",
		Short:   preCommitWrapperShort,
		Long:    preCommitWrapperLong,
		Example: preCommitWrapperExample,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return status(preCommitRun(cmd, timeout, func() workResult {
				return workResult{out: []byte(wrapperWork(config, module, wrapper) + "\n"), code: exitClean}
			}))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&config, "config", "", preCommitConfigUsage)
	f.StringVar(&module, "module", "", preCommitModuleUsage)
	f.StringVar(&wrapper, "wrapper", "", preCommitWrapperUsage)
	addTimeoutFlag(cmd, &timeout)
	for _, n := range []string{"config", "module", "wrapper"} {
		_ = cmd.MarkFlagRequired(n)
	}
	return cmd
}

// wrapperWork is the result line of pre-commit wrapper: a file it cannot
// read first, the wrapper before the module, then the pin and the pair.
func wrapperWork(config, module, wrapper string) string {
	w, why := readInput(wrapper, facts.MaxWrapperFileBytes)
	if why != "" {
		return "unknown wrapper " + why
	}
	m, why := readInput(module, facts.MaxWrapperFileBytes)
	if why != "" {
		return "unknown module " + why
	}
	pin := facts.PinUnreadable
	switch c, why := readInput(config, facts.MaxWrapperFileBytes); why {
	case "":
		pin = facts.WrapperPin(c)
	case inputAbsent:
		pin = facts.PinNone
	}
	return facts.WrapperResult(pin, m, w)
}

var docsVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

func newPreCommitDocsCmd() *cobra.Command {
	var (
		config, workflow, version string
		timeout                   time.Duration
	)
	cmd := &cobra.Command{
		Use:     "docs --config <file> [--workflow <file> --version <vX.Y.Z>]",
		Short:   preCommitDocsShort,
		Long:    preCommitDocsLong,
		Example: preCommitDocsExample,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (workflow == "") != (version == "") {
				return status(usageError(cmd, preCommitDocsPair))
			}
			if version != "" && !docsVersionPattern.MatchString(version) {
				return status(usageError(cmd, preCommitDocsVersion))
			}
			return status(preCommitRun(cmd, timeout, func() workResult {
				return workResult{out: []byte(docsWork(config, workflow, version)), code: exitClean}
			}))
		},
	}
	f := cmd.Flags()
	f.SortFlags = false
	f.StringVar(&config, "config", "", preCommitConfigUsage)
	f.StringVar(&workflow, "workflow", "", preCommitWorkflowUsage)
	f.StringVar(&version, "version", "", preCommitVersionUsage)
	addTimeoutFlag(cmd, &timeout)
	_ = cmd.MarkFlagRequired("config")
	return cmd
}

// docsWork is the output of pre-commit docs: the hook line, then the
// workflow line when a workflow is given.
func docsWork(config, workflow, version string) string {
	hook := facts.DocsHookCustom
	if c, why := readInput(config, facts.MaxDocsFileBytes); why == "" {
		hook = facts.DocsHook(c)
	}
	out := "hook " + hook + "\n"
	if workflow == "" {
		return out
	}
	wf := facts.WorkflowUnreadable
	switch c, why := readInput(workflow, facts.MaxDocsFileBytes); why {
	case "":
		wf = facts.DocsWorkflowPin(c, version)
	case inputAbsent:
		wf = facts.WorkflowNone
	}
	return out + "workflow " + wf + "\n"
}

// inputAbsent is the reason readInput gives for a path with nothing at it.
const inputAbsent = "absent"

// readInput reads one input file under limit. why is empty when it was read,
// and otherwise the reason it was not: absent, link, not a regular file,
// over size or read failed. The host extracts each input from the head, so
// nothing at the path means the file is absent there.
func readInput(p string, limit int64) ([]byte, string) {
	st, err := os.Lstat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, inputAbsent
	case err != nil:
		return nil, "read failed"
	case st.Mode()&fs.ModeSymlink != 0:
		return nil, "link"
	case !st.Mode().IsRegular():
		return nil, "not a regular file"
	}
	data, err := readcap.Prefix(p, limit)
	switch {
	case err != nil:
		return nil, "read failed"
	case int64(len(data)) > limit:
		return nil, "over size"
	}
	return data, ""
}
