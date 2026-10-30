// Command mtf-facts reports deterministic facts about Terraform module trees.
// It reads files only: no network, no Terraform, no other program.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// IDs of the command groups of the root help.
const (
	groupCheck    = "check"
	groupPipeline = "pipeline"
	groupOther    = "other"
)

func init() {
	// The help lists commands in the order they are added.
	cobra.EnableCommandSorting = false
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command line in args and returns the exit code. A handler
// writes its own messages; an error of cobra's (a flag, an argument count, an
// unknown command or help topic) is written here and exits 2.
func run(args []string, stdout, stderr io.Writer) int {
	root := newRootCmd(stdout, stderr)
	root.SetArgs(append([]string{}, args...)) // never nil: cobra would read os.Args
	cmd, err := root.ExecuteC()
	if err == nil {
		return exitClean
	}
	if st, ok := errors.AsType[exitStatus](err); ok {
		return st.code
	}
	fmt.Fprintf(stderr, "mtf-facts: %v\n"+usageHint, err, cmd.CommandPath())
	return exitError
}

// exitStatus is the exit code a handler chose after writing its own message.
type exitStatus struct{ code int }

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// status turns a handler's exit code into what its RunE returns.
func status(code int) error {
	if code == exitClean {
		return nil
	}
	return exitStatus{code}
}

// usageError writes a usage error a handler found, as run writes cobra's,
// and returns exitError.
func usageError(cmd *cobra.Command, msg string) int {
	fmt.Fprintf(cmd.ErrOrStderr(), "mtf-facts: %s\n"+usageHint, msg, cmd.CommandPath())
	return exitError
}

// newRootCmd builds the command tree. Each run builds its own, so no flag
// value outlives a run.
func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	var showVersion bool
	root := &cobra.Command{
		Use:     "mtf-facts <command>",
		Short:   rootShort,
		Long:    rootLong,
		Example: rootExample,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if showVersion {
				fmt.Fprintln(cmd.OutOrStdout(), version())
				return nil
			}
			return usageExit(cmd, nil)
		},
	}
	// Set before InitDefaultCompletionCmd, which keeps the writer it sees.
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetUsageTemplate(usageTemplate)
	root.Flags().SortFlags = false
	root.InitDefaultHelpFlag() // listed before --version
	root.Flags().BoolVar(&showVersion, "version", false, rootVersionUsage)

	root.AddGroup(
		&cobra.Group{ID: groupCheck, Title: groupCheckTitle},
		&cobra.Group{ID: groupPipeline, Title: groupPipelineTitle},
		&cobra.Group{ID: groupOther, Title: groupOtherTitle},
	)
	root.AddCommand(newLintCmd(), newLedgerCmd(), newSchemaDiffCmd(), newAffectedCmd(), newBundleCmd(), newMergeCmd(), newFixtureCheckCmd(), newRulesCmd(), newVersionCmd())
	root.SetHelpCommand(newHelpCmd(root))
	root.SetHelpCommandGroupID(groupOther)
	root.SetCompletionCommandGroupID(groupOther)
	// Both are added here, not in Execute, so silence reaches them. Execute
	// moves help to the end of the list again.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			c.Short = completionShort
			// Runnable, so a bare or unknown subcommand exits 2 as merge does.
			c.Args, c.RunE = cobra.NoArgs, usageExit
		}
	}
	silence(root)
	return root
}

// usageExit is the RunE of a parent command run bare: its usage goes to
// stderr and the run exits 2.
func usageExit(cmd *cobra.Command, _ []string) error {
	fmt.Fprint(cmd.ErrOrStderr(), cmd.UsageString())
	return exitStatus{exitError}
}

// silence leaves every error and usage message to run and the handlers.
func silence(c *cobra.Command) {
	c.SilenceErrors, c.SilenceUsage = true, true
	for _, sub := range c.Commands() {
		silence(sub)
	}
}

// newHelpCmd prints the help of root or of the command its arguments name,
// to stdout. Arguments that name no command are an unknown help topic.
func newHelpCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: helpShort,
		Long:  helpLong,
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			c, rest, err := root.Find(args)
			if err != nil || len(rest) > 0 {
				return fmt.Errorf("unknown help topic %q", strings.Join(args, " "))
			}
			c.InitDefaultHelpFlag()
			return c.Help()
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   versionShort,
		GroupID: groupOther,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), version())
			return nil
		},
	}
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}
