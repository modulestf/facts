package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/spf13/cobra"
)

// defaultTimeout is the --timeout of every command that has the flag, and
// the bound of those that have none.
const defaultTimeout = 60 * time.Second

// workResult is what a command's worker hands back. Only runBounded writes
// to the command's streams.
type workResult struct {
	out   []byte   // stdout, refused when over the output cap
	notes []string // stderr lines, written before out
	code  int      // the exit code when err is nil
	err   error    // written as "mtf-facts: <err>"; the run exits 2
	// commit, when set, runs after the cap check and before out is written:
	// bundle writes its files there, so a timeout never leaves half of them.
	commit func() error
}

// testHookWork, when set, runs in the worker before the work; tests set it
// to hold the worker past the timeout.
var testHookWork func()

// addTimeoutFlag adds --timeout to cmd.
func addTimeoutFlag(cmd *cobra.Command, timeout *time.Duration) {
	cmd.Flags().DurationVar(timeout, "timeout", defaultTimeout, lintTimeoutUsage)
}

// runBounded runs work in a worker and waits at most timeout for it. It
// returns the exit code. A timeout that is not positive is a usage error.
// lim.Deadline is set as defense in depth: the timer is the mechanism, and
// the engine's own deadline error reads as the timeout. When the timer fires
// first, late gives the result if it is set (depth fails closed with a
// report); otherwise the run writes "mtf-facts: timeout" and exits 2. main's
// os.Exit ends a worker still running, which may be stuck in a parser. The
// output cap is lim.MaxOutputBytes.
func runBounded(cmd *cobra.Command, timeout time.Duration, lim *facts.Limits, work, late func() workResult) int {
	stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if timeout <= 0 {
		return usageError(cmd, timeoutNotPositive)
	}
	lim.Deadline = time.Now().Add(timeout)
	hook := testHookWork             // read here, not in the worker, which may outlive the test
	done := make(chan workResult, 1) // buffered: a late worker never blocks
	go func() {
		if hook != nil {
			hook()
		}
		done <- work()
	}()
	timer := time.NewTimer(timeout)
	var res workResult
	select {
	case res = <-done:
		timer.Stop()
	case <-timer.C:
		lim.Stats.Timeout()
		if late == nil {
			fmt.Fprint(stderr, "mtf-facts: timeout\n")
			return exitError
		}
		res = late()
	}
	switch {
	case errors.Is(res.err, facts.ErrDeadline):
		lim.Stats.Timeout()
		fmt.Fprint(stderr, "mtf-facts: timeout\n")
		return exitError
	case res.err != nil:
		fmt.Fprintln(stderr, "mtf-facts:", res.err)
		return exitError
	case len(res.out) > lim.MaxOutputBytes:
		fmt.Fprintf(stderr, "mtf-facts: output over %d bytes\n", lim.MaxOutputBytes)
		return exitError
	}
	if res.commit != nil {
		if err := res.commit(); err != nil {
			fmt.Fprintln(stderr, "mtf-facts:", err)
			return exitError
		}
	}
	for _, n := range res.notes {
		fmt.Fprintln(stderr, n)
	}
	if _, err := stdout.Write(res.out); err != nil {
		return exitError
	}
	return res.code
}

// jsonResult is the result of a worker whose output is v as indented JSON.
func jsonResult(v any, code int) workResult {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return workResult{err: err}
	}
	return workResult{out: append(out, '\n'), code: code}
}
